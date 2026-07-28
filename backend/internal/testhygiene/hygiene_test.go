// Package testhygiene is a STATIC gate over the backend's own test suite: every
// place a test brings a company into existence must also hand that company to
// internal/platform/testdb for teardown.
//
// WHY A SECOND LAYER AT ALL. backend/scripts/test_residue.sh already proves the
// bound empirically — it counts rows before and after a full run and fails on any
// growth. That layer is the stronger evidence, but it has two properties this one
// does not:
//
//   - it needs a live, QUIESCENT database. This project shares one dev database
//     between concurrent agents, so a row-count delta cannot distinguish "this
//     suite leaked" from "another agent inserted while we ran". The empirical gate
//     is therefore something you run deliberately, alone.
//   - it only fires AFTER the leak has happened. By then the rows exist.
//
// This file needs no database, no quiescence and no cooperation from anyone else.
// It parses the tree and fails on the 686th creation site the day it is WRITTEN.
// That is the whole point: the previous 685-companies-per-run state of affairs
// was not one bad commit, it was a hundred commits each of which individually
// looked fine, because nothing in the ordinary suite could object.
//
// The directory holds ONLY this _test.go file. archtest's boundary walker skips
// _test.go files (internal/archtest/boundaries_test.go), so this package needs no
// allowedDeps entry — and, having no non-test file, it can never be linked into
// cored.
package testhygiene

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// The opt-out registry. EMPTY, and asserted empty.
// ---------------------------------------------------------------------------

// declaredKeeps maps "<path relative to backend/>:<line>" of every
// testdb.Keep call site to the reason that company must OUTLIVE the test.
//
// IT IS EMPTY, AND TestOptOutsAreDeclared ASSERTS THAT IT IS EMPTY.
//
// That is a finding, not an oversight. The design review asked which Go tests
// need their data to survive, audited the candidates, and the answer was none:
//
//   - t.Cleanup runs AFTER the test body, so nothing a test asserts during its
//     own run can be made vacuous by deleting the data afterwards. The tombstone
//     and hash-chain tests (internal/server/voucher_history_test.go) assert
//     inside the test; the tamper test restores the chain before returning.
//   - a test would only need an opt-out if data had to survive its own
//     COMPLETION, which requires a TestMain-level fixture, a package-level shared
//     company, or an order dependency between tests. The backend has none of the
//     three: `grep -rn "TestMain" --include='*_test.go' internal/ cmd/` and
//     `grep -rn "^var .*[Cc]ompany" --include='*_test.go' internal/` are both
//     empty. Every harness mints its company inside the test that uses it.
//   - the only genuinely long-lived fixtures are the live-gate seeds
//     (frontend/scripts/seed-demo.mts, seed-tenancy.mts). They are TypeScript,
//     they create nothing through the Go harness, and they are unaffected.
//
// Keep() nevertheless ships, and this map is why it is not dead code. Adding a
// Keep anywhere forces the author to write down WHERE and WHY, in a file a
// reviewer reads, or this test goes red. The alternative — "just omit the cleanup
// call" — makes a deliberate exception indistinguishable from a forgotten one,
// and the second kind is exactly how 685 companies per run accumulated unnoticed.
var declaredKeeps = map[string]string{}

// excludedPackages is NOT a whitelist of leaking sites. It holds the ONE package
// that cannot satisfy the rule by construction: internal/platform/testdb is the
// teardown machinery itself, its tests are `package testdb`, and an internal test
// therefore cannot reference the machinery through a `testdb.` selector at all.
// Its fixtures deliberately hand-write `INSERT INTO company` rows in a pure
// builder (buildFixture) that has no business calling Drop.
//
// The exclusion is safe because that package is the one place BOTH layers cover:
// testdb_test.go asserts its own residue directly (it re-counts every planned
// table inside the drop transaction), and the empirical gate's ./... run measures
// it like every other package.
//
// TestEveryCompanyCreatingTestSiteTearsDown asserts this list has EXACTLY ONE
// entry, so it cannot quietly grow into the whitelist this design refuses to have.
var excludedPackages = map[string]string{
	"internal/platform/testdb": "the teardown machinery under test; `package testdb` cannot reference `testdb.`",
}

// selfPackage is this gate's own source, and skipping it is not an exemption in
// the sense excludedPackages is: this package touches no database and imports
// nothing that could create a company (go/ast, go/parser, os, regexp — that is
// the whole list). It has to be skipped for the mechanical reason that a detector
// must QUOTE the patterns it detects: `insertIntoCompany`'s regexp source and the
// kind strings below both contain the literal text "INSERT INTO company", and the
// walker would otherwise report itself.
const selfPackage = "internal/testhygiene"

// ---------------------------------------------------------------------------
// Site detection
// ---------------------------------------------------------------------------

// insertIntoCompany matches a raw SQL company insert, whitespace-tolerant,
// case-insensitive, and schema-qualified or not. The trailing \b matters: without
// it this also fires on `INSERT INTO company_membership` / `company_invite`,
// which are children of a company rather than a company, and flagging them would
// train people to ignore this test.
var insertIntoCompany = regexp.MustCompile(`(?i)insert\s+into\s+(?:public\s*\.\s*)?"?company"?\b`)

// companiesCollection is the CREATE route: POST to the companies COLLECTION.
//
// EXACT equality, deliberately. Two weaker spellings were tried and both are
// wrong in the same direction — they flag functions that never create anything:
//
//	strings.Contains(lit, "/v1/companies")  -> matches every voucher, report and
//	                                           member route in internal/server.
//	strings.TrimSuffix(lit, "/") == ...     -> matches the "/v1/companies/" prefix
//	                                           literal that every
//	                                           `"/v1/companies/"+companyID+"/x"`
//	                                           concatenation is built from. That
//	                                           alone produced 60 false positives
//	                                           across internal/server.
//
// A gate that cries wolf is a gate people delete, so the rule is the narrow one.
func companiesCollection(lit string) bool {
	return lit == "/v1/companies"
}

type site struct {
	pkg      string // path relative to backend/internal, e.g. "server"
	file     string // path relative to backend/, e.g. "internal/server/auth_test.go"
	funcLine int    // line of the enclosing FuncDecl
	funcName string
	siteLine int    // line of the creation site itself
	kind     string // human-readable: what made this a creation site
	torndown bool   // the enclosing FuncDecl references testdb.
}

func (s site) String() string {
	return fmt.Sprintf("%s:%d %s  (creation site at line %d: %s)",
		s.file, s.funcLine, s.funcName, s.siteLine, s.kind)
}

// internalRoot is backend/internal, relative to this test's working directory
// (go test runs each package's tests in that package's own directory).
const internalRoot = ".."

// scan parses every _test.go under backend/internal and returns every company
// creation site it can find, each already marked with whether its enclosing
// function reaches for the teardown helper.
//
// It also returns the number of package-level string literals that looked like a
// creation site but had no enclosing function to attribute them to. Those cannot
// be judged (a shared const used by a well-behaved helper is fine), so rather
// than guess, the caller fails and asks a human to look — see the assertion in
// TestEveryCompanyCreatingTestSiteTearsDown.
func scan(t *testing.T) (sites []site, keeps map[string]string, unattributed []string) {
	t.Helper()
	keeps = map[string]string{}

	fset := token.NewFileSet()
	err := filepath.WalkDir(internalRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		rel := filepath.ToSlash(filepath.Join("internal", mustRel(t, internalRoot, path)))
		pkg := filepath.ToSlash(filepath.Dir(rel))
		if _, skip := excludedPackages[pkg]; skip || pkg == selfPackage {
			return nil
		}

		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		f, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			return fmt.Errorf("parse %s: %w", rel, err)
		}

		// testdb.Keep call sites are collected file-wide, independent of whether
		// the enclosing function is a creation site.
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Keep" && isIdent(sel.X, "testdb") {
				keeps[fmt.Sprintf("%s:%d", rel, fset.Position(call.Pos()).Line)] = ""
			}
			return true
		})

		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			found := sitesIn(fset, fn)
			if len(found) == 0 {
				continue
			}
			torndown := referencesTestdb(fn)
			for _, s := range found {
				s.pkg = strings.TrimPrefix(pkg, "internal/")
				s.file = rel
				s.funcLine = fset.Position(fn.Pos()).Line
				s.funcName = fn.Name.Name
				s.torndown = torndown
				sites = append(sites, s)
			}
		}

		// Anything that looks like a creation site OUTSIDE any function body.
		for _, decl := range f.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			ast.Inspect(gen, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				v := unquote(lit.Value)
				if insertIntoCompany.MatchString(v) || companiesCollection(v) {
					unattributed = append(unattributed,
						fmt.Sprintf("%s:%d (package-level literal %q)", rel, fset.Position(lit.Pos()).Line, truncate(v)))
				}
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", internalRoot, err)
	}

	sort.Slice(sites, func(i, j int) bool {
		if sites[i].file != sites[j].file {
			return sites[i].file < sites[j].file
		}
		return sites[i].siteLine < sites[j].siteLine
	})
	return sites, keeps, unattributed
}

// sitesIn returns the creation sites inside one function body.
func sitesIn(fset *token.FileSet, fn *ast.FuncDecl) []site {
	var out []site
	var postLits []*ast.BasicLit
	hasMethodPost := false

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			// (a) a call to CreateCompany / createCompany, however it is
			// qualified: ids.CreateCompany(...), identity.NewService(p).CreateCompany(...),
			// or a bare createCompany(...) helper in the same package.
			switch f := x.Fun.(type) {
			case *ast.SelectorExpr:
				if f.Sel.Name == "CreateCompany" || f.Sel.Name == "createCompany" {
					out = append(out, site{
						siteLine: fset.Position(x.Pos()).Line,
						kind:     "call to " + f.Sel.Name,
					})
				}
			case *ast.Ident:
				if f.Name == "CreateCompany" || f.Name == "createCompany" {
					out = append(out, site{
						siteLine: fset.Position(x.Pos()).Line,
						kind:     "call to " + f.Name,
					})
				}
			}
		case *ast.SelectorExpr:
			if isIdent(x.X, "http") && x.Sel.Name == "MethodPost" {
				hasMethodPost = true
			}
		case *ast.BasicLit:
			if x.Kind != token.STRING {
				return true
			}
			v := unquote(x.Value)
			// (b) a raw SQL insert.
			if insertIntoCompany.MatchString(v) {
				out = append(out, site{
					siteLine: fset.Position(x.Pos()).Line,
					kind:     "raw INSERT INTO company",
				})
			}
			// (c) the create ROUTE. Recorded now, promoted to a site below only
			// if this same function also mentions http.MethodPost — the
			// simplification that keeps this a 60-line AST walk instead of a
			// call-graph analysis, at the cost of also flagging a function that
			// POSTs elsewhere and merely names the collection path. That
			// direction of error is the safe one.
			if companiesCollection(v) {
				postLits = append(postLits, x)
			}
		}
		return true
	})

	if hasMethodPost {
		for _, lit := range postLits {
			out = append(out, site{
				siteLine: fset.Position(lit.Pos()).Line,
				kind:     "POST to /v1/companies",
			})
		}
	}
	return out
}

// referencesTestdb reports whether the function body reaches for the teardown
// package at all — testdb.Cleanup, testdb.DropNow or testdb.Keep.
//
// Keying on the ENCLOSING FUNCTION rather than on the Test* function is
// deliberate. It means a shared setup helper is checked once, where the fix
// belongs, AND that an inline second-tenant creation inside a single test is
// checked separately. That second case is the one every retrofit misses:
// internal/ledger/isolation_test.go, internal/query/query_security_test.go,
// internal/reporting/stock_summary_test.go and internal/masters/inventory_masters_test.go
// all mint a SECOND company in the test body to prove cross-tenant invisibility,
// and a rule keyed on "the harness cleaned up" would have let all four leak.
func referencesTestdb(fn *ast.FuncDecl) bool {
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if found {
			return false
		}
		if sel, ok := n.(*ast.SelectorExpr); ok && isIdent(sel.X, "testdb") {
			found = true
			return false
		}
		return true
	})
	return found
}

func isIdent(e ast.Expr, name string) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == name
}

func unquote(lit string) string {
	if s, err := strconv.Unquote(lit); err == nil {
		return s
	}
	// Raw string literals with embedded newlines unquote fine; anything that
	// does not is returned as-is so a matcher still gets a chance at it.
	return lit
}

func truncate(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 60 {
		return s[:60] + "..."
	}
	return s
}

func mustRel(t *testing.T, base, path string) string {
	t.Helper()
	rel, err := filepath.Rel(base, path)
	if err != nil {
		t.Fatalf("rel(%s, %s): %v", base, path, err)
	}
	return rel
}

// ---------------------------------------------------------------------------
// The gates
// ---------------------------------------------------------------------------

// TestEveryCompanyCreatingTestSiteTearsDown is the static half of the ZERO
// residue bound. There is NO whitelist and no "pending adoption" set on purpose:
// if this is red, adopt the site, do not exempt it. A pending list is how the
// previous cleanup rotted — it deleted voucher_version and company and left the
// entire chart of accounts orphaned (603 MB of account_group), and nothing in the
// suite could say so.
func TestEveryCompanyCreatingTestSiteTearsDown(t *testing.T) {
	if len(excludedPackages) != 1 {
		t.Fatalf("excludedPackages has %d entries, want exactly 1 (internal/platform/testdb).\n"+
			"This list is NOT a whitelist. A package added here stops being checked for\n"+
			"leaks entirely, which is the failure mode this whole gate exists to prevent.\n"+
			"got: %v", len(excludedPackages), excludedPackages)
	}

	sites, _, unattributed := scan(t)

	if len(unattributed) > 0 {
		t.Errorf("%d company-creation string literal(s) live OUTSIDE any function, so this\n"+
			"gate cannot tell whether they are torn down. Move them into the function that\n"+
			"uses them, or teach the walker how to attribute them — do not leave them:\n  %s",
			len(unattributed), strings.Join(unattributed, "\n  "))
	}

	var bad []string
	for _, s := range sites {
		if !s.torndown {
			bad = append(bad, s.String())
		}
	}
	if len(bad) > 0 {
		t.Errorf("%d company-creation site(s) in the test suite do not tear down.\n\n"+
			"Each of these puts a company into the SHARED dev database and leaves it there.\n"+
			"That is not untidiness: it is what took the database to 43,419 companies and\n"+
			"2010 MB, made e2e:ui flaky (one login accumulated 120 memberships and the F3\n"+
			"picker rendered all of them), made a migration backfill run 1h57m, and ended a\n"+
			"ledger test run with \"No space left on device\".\n\n"+
			"FIX: in the function named below, capture the company's uuid.UUID and add\n"+
			"    testdb.Cleanup(t, companyID)\n"+
			"(or testdb.DropNow for an immediate drop). If the company genuinely must\n"+
			"outlive the test, use testdb.Keep(t, id, reason) AND declare the call site in\n"+
			"declaredKeeps in this file — see TestOptOutsAreDeclared.\n\n"+
			"  %s", len(bad), strings.Join(bad, "\n  "))
	}
}

// TestOptOutsAreDeclared keeps the escape hatch honest: every testdb.Keep call
// site in the tree must be listed in declaredKeeps with a written reason, and
// every entry in declaredKeeps must correspond to a real call site (so the map
// cannot accumulate stale entries that quietly bless a line that moved).
func TestOptOutsAreDeclared(t *testing.T) {
	_, keeps, _ := scan(t)

	var undeclared []string
	for loc := range keeps {
		if _, ok := declaredKeeps[loc]; !ok {
			undeclared = append(undeclared, loc)
		}
	}
	sort.Strings(undeclared)
	if len(undeclared) > 0 {
		t.Errorf("testdb.Keep called at %d site(s) that declaredKeeps does not list:\n  %s\n\n"+
			"A kept company stays in the shared dev database forever. Add an entry to\n"+
			"declaredKeeps in this file saying WHY, so the next person reading the residue\n"+
			"numbers can tell a deliberate exception from a forgotten cleanup.",
			len(undeclared), strings.Join(undeclared, "\n  "))
	}

	var stale []string
	for loc, reason := range declaredKeeps {
		if _, ok := keeps[loc]; !ok {
			stale = append(stale, fmt.Sprintf("%s (%q)", loc, reason))
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf("declaredKeeps lists %d site(s) with no matching testdb.Keep call:\n  %s\n\n"+
			"Line numbers move. A stale entry is a pre-authorised exemption sitting on a\n"+
			"line that now does something else — delete it.",
			len(stale), strings.Join(stale, "\n  "))
	}

	// The state this project decided on, recorded as an assertion rather than a
	// comment so that changing it is a deliberate edit to a test.
	if len(declaredKeeps) != 0 {
		t.Errorf("declaredKeeps is expected to be EMPTY (see its doc comment: no Go test needs\n"+
			"its data to survive its own completion), but has %d entry/entries: %v.\n"+
			"If that is now genuinely wrong, change this assertion in the same commit as the\n"+
			"Keep, and say in the message which test needs a company to outlive it and why.",
			len(declaredKeeps), declaredKeeps)
	}
}

// TestHygieneWalkerActuallyFindsSites is the anti-vacuity guard, and it is not a
// formality: a walker that silently found nothing — a wrong root path, a renamed
// helper, a parse error swallowed — would report a clean bill of health forever,
// which is precisely the class of "test that cannot fail" this project keeps
// getting burned by. The floors are set well below the ~28 sites across 11
// packages present when this was written, so ordinary churn does not trip them,
// but deleting the detection rules does.
func TestHygieneWalkerActuallyFindsSites(t *testing.T) {
	sites, _, _ := scan(t)

	pkgs := map[string]bool{}
	kinds := map[string]int{}
	for _, s := range sites {
		pkgs[s.pkg] = true
		kinds[s.kind]++
	}

	const (
		minSites = 25
		minPkgs  = 10
	)
	if len(sites) < minSites {
		t.Errorf("walker found only %d company-creation sites, want >= %d.\n"+
			"Either the detection rules stopped matching or the walk root (%q) is wrong.\n"+
			"A walker that finds nothing makes TestEveryCompanyCreatingTestSiteTearsDown\n"+
			"pass unconditionally. kinds found: %v", len(sites), minSites, internalRoot, kinds)
	}
	if len(pkgs) < minPkgs {
		names := make([]string, 0, len(pkgs))
		for p := range pkgs {
			names = append(names, p)
		}
		sort.Strings(names)
		t.Errorf("walker found sites in only %d packages, want >= %d: %v",
			len(pkgs), minPkgs, names)
	}

	// All three detection rules must still be live. If one stops matching, the
	// totals above could still clear their floors on the strength of the other
	// two while a whole class of creation site went unpoliced.
	for _, kind := range []string{"call to CreateCompany", "raw INSERT INTO company", "POST to /v1/companies"} {
		if kinds[kind] == 0 {
			t.Errorf("detection rule %q matched nothing anywhere in the tree — it has rotted.\n"+
				"kinds found: %v", kind, kinds)
		}
	}
	t.Logf("walker found %d creation sites across %d packages; kinds=%v", len(sites), len(pkgs), kinds)
}
