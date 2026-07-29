package payrollfile

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// flatten strips Go comment markers and collapses whitespace so a phrase can be
// found across a line wrap.
func flatten(s string) string {
	s = strings.ReplaceAll(s, "//", " ")
	return strings.Join(strings.Fields(s), " ")
}

// TestPayrollFileDisclosureIsPresent reads doc.go's SOURCE and fails if the
// not-validator-verified statement is missing or has been softened.
//
// This is a test about a sentence, and it earns its place because the failure
// mode here is social rather than technical. Nothing breaks when the disclosure
// is trimmed to "not yet validated" by someone who finds the wording alarming
// before a demo — and after that trim, an operator who reads the doc believes
// the file was checked by the FVU. It was not, and it cannot be: the FVU is a
// Java desktop utility and this machine has no network. Every phrase asserted
// below carries a distinct part of the claim, so softening any one of them
// fails the build rather than quietly changing what we tell people.
func TestPayrollFileDisclosureIsPresent(t *testing.T) {
	// The PACKAGE DOC COMMENT ONLY — not the whole file.
	//
	// The first version of this test read doc.go as raw bytes, and a mutation
	// that deleted the disclosure from the package comment did NOT fail it: the
	// exported Disclosure constant is declared in the same file, so its string
	// literal satisfied every phrase check while the doc a reader actually sees
	// (`go doc`, the editor hover, the top of the file) had been gutted. The two
	// places are checked separately below precisely because deleting either one
	// must bite on its own.
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, "doc.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse doc.go: %v", err)
	}
	if parsed.Doc == nil {
		t.Fatal("doc.go has no package doc comment at all")
	}
	// flatten survives a re-wrap of the comment block (the disclosure is long
	// and gofmt-wrapped across lines) but not a deletion or a reword.
	doc := flatten(parsed.Doc.Text())

	// The load-bearing fragments of the disclosure sentence itself.
	for _, phrase := range []string{
		"has NOT been checked",
		"FVU",
		"EPFO Unified",
		"ESIC employer portal",
		"not evidence of it",
		"golden corpus is a substitute for validation",
	} {
		if !strings.Contains(doc, phrase) {
			t.Errorf("doc.go no longer contains %q: the not-validator-verified disclosure has been removed or softened, and the next reader will believe this output was validated", phrase)
		}
	}

	// The documented gaps. Each of these is a thing the software does NOT do
	// that a reader would otherwise assume it does.
	for _, gap := range []string{
		"Q4",
		"Annexure II",
		"not published",
		"BIFF8",
		"EDLI",
		"to be notified separately",
		"Professional Tax",
	} {
		// Case-insensitive: the doc SHOUTS several of these ("NOT PUBLISHED",
		// "PROFESSIONAL TAX") and the emphasis is free to change. The disclosure
		// phrases above are checked case-sensitively because their casing —
		// "has NOT been checked" — is the emphasis.
		if !strings.Contains(strings.ToLower(doc), strings.ToLower(gap)) {
			t.Errorf("doc.go no longer documents the gap %q; an undocumented gap is a claim we did not mean to make", gap)
		}
	}

	// The same statement must be reachable at RUNTIME, not only in a comment,
	// because the operator downloading the file never reads doc.go.
	flatDisclosure := flatten(Disclosure)
	for _, phrase := range []string{"has NOT been checked", "FVU", "not evidence of it"} {
		if !strings.Contains(flatDisclosure, phrase) {
			t.Errorf("the exported Disclosure constant no longer contains %q", phrase)
		}
	}
	var buf bytes.Buffer
	if err := WriteDisclosureSidecar(&buf, "ecr-202607.txt"); err != nil {
		t.Fatalf("WriteDisclosureSidecar: %v", err)
	}
	if !strings.Contains(buf.String(), "ecr-202607.txt") || !strings.Contains(buf.String(), "has NOT been checked") {
		t.Errorf("the sidecar does not name the file it describes or does not carry the disclosure:\n%s", buf.String())
	}
	if err := WriteDisclosureSidecar(&buf, ""); err == nil {
		t.Error("a sidecar with no file name was written; a disclosure that names nothing describes nothing")
	}
}

// TestPayrollFileImportsNoInternalPackage is the archtest rule, restated where
// it cannot be relaxed by editing a map in another package.
//
// internal/payroll/payrollfile's allowed-dependency set in archtest is the EMPTY
// MAP, deliberately, and this test is the second lock on it. If this package
// could import internal/payroll, a file format's field list would start
// dictating engine semantics — the ECR's "EPF & EPS Diff" column would become a
// field on the PF result, the Form 138 DD layout would leak into the income-tax
// result — and the day the Q4 format is finally published the engine would have
// to change to accommodate a file. The emptiness is what keeps these writers
// replaceable.
func TestPayrollFileImportsNoInternalPackage(t *testing.T) {
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no .go files found; this test would pass vacuously")
	}
	checked := 0
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		checked++
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, imp := range f.Imports {
			ip := strings.Trim(imp.Path.Value, `"`)
			if strings.Contains(ip, "/internal/") {
				t.Errorf("%s imports %q: payrollfile must import NO internal package, so that a file format can never dictate engine semantics", path, ip)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no non-test .go files were checked; the scan is vacuous")
	}
	t.Logf("scanned %d non-test files, no internal imports", checked)
}

// TestPayrollPackagesDoNotLog scans EVERY payroll package — the engine, the
// db-facing service and this one — and fails on any logging import or any print
// to stdout/stderr.
//
// Payroll handles the most disclosure-sensitive data in the product: an
// employee's salary, PAN, UAN and ESIC IP number. A log line is a copy of that
// data in a file with no access control, no retention policy and no tenant
// scoping, which will be shipped to whoever debugs the next incident. There is
// no redaction boundary in the Go plane to catch it — insightd's recognizers are
// Python and cover only outbound LLM calls — so the only workable rule is that
// this data never reaches a logger at all, and the only way to keep that rule is
// to make it mechanical.
//
// The scan starts at internal/payroll and covers whatever packages exist under
// it, so a package added later is covered the day it appears without anyone
// remembering to extend this test.
func TestPayrollPackagesDoNotLog(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if base := filepath.Base(root); base != "payroll" {
		t.Fatalf("expected to be running inside internal/payroll/..., got root %q", root)
	}

	fset := token.NewFileSet()
	scanned := 0
	walkErr := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		scanned++

		for _, imp := range f.Imports {
			switch strings.Trim(imp.Path.Value, `"`) {
			case "log", "log/slog":
				t.Errorf("%s imports a logger: payroll data (salary, PAN, UAN, IP number) must never reach a log file, and there is no redaction boundary in the Go plane to catch it", rel)
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
			name := sel.Sel.Name
			// fmt.Print/Printf/Println always write to stdout.
			if strings.HasPrefix(name, "Print") {
				t.Errorf("%s calls fmt.%s: that writes payroll data to stdout", rel, name)
				return true
			}
			// fmt.Fprint* is fine to an io.Writer the caller owns, and is how
			// these emitters write files; it is NOT fine to the process's own
			// streams.
			if strings.HasPrefix(name, "Fprint") && len(call.Args) > 0 {
				if dst, ok := call.Args[0].(*ast.SelectorExpr); ok {
					if x, ok := dst.X.(*ast.Ident); ok && x.Name == "os" &&
						(dst.Sel.Name == "Stdout" || dst.Sel.Name == "Stderr") {
						t.Errorf("%s calls fmt.%s to os.%s: that writes payroll data to the process log", rel, name, dst.Sel.Name)
					}
				}
			}
			return true
		})
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk %s: %v", root, walkErr)
	}
	if scanned == 0 {
		t.Fatal("no payroll source files were scanned; the check is vacuous")
	}
	t.Logf("scanned %d non-test files under internal/payroll for logging", scanned)
}
