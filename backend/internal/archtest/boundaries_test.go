// Package archtest enforces the modular-monolith import boundaries in CI. The
// big-ball-of-mud risk (PLAN.md A.6) is structural and un-retrofittable, so we
// assert the allowed cross-internal import graph mechanically. A new forbidden
// edge fails the build.
package archtest

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const modulePath = "github.com/khata/cored"

// allowedDeps maps each business module to the set of sibling internal packages
// it MAY import. The platform/db package is universally importable (it is the
// shared infrastructure seam). compliance is a pure leaf and imports no
// siblings. ledger/reporting/identity may use platform/db only. server is the
// composition root and may import any module. Anything not listed is forbidden.
var allowedDeps = map[string]map[string]bool{
	"internal/compliance":  {},
	// gstr is a read-only GST return generator (GSTR-1) over the tenant-scoped
	// db layer. It does not import the parent compliance package.
	"internal/compliance/gstr": {"internal/platform/db": true},
	"internal/inventory":       {},
	"internal/integration": {},
	"internal/identity":    {"internal/platform/db": true},
	// ledger derives GST inline during a Sales/Purchase post, so it may import
	// the pure compliance leaf (no I/O, safe inside the post transaction).
	"internal/ledger":      {"internal/platform/db": true, "internal/compliance": true},
	"internal/masters":     {"internal/platform/db": true},
	"internal/reporting":   {"internal/platform/db": true},
	// migration is the Tally-XML importer + reconciliation harness: it parses
	// via integration, ensures masters, posts through ledger, and reconciles via
	// reporting. It is an orchestration leaf — nothing imports it except server.
	"internal/migration": {
		"internal/integration": true,
		"internal/masters":     true,
		"internal/ledger":      true,
		"internal/reporting":   true,
	},
	"internal/platform/db": {},
	"internal/server": {
		"internal/platform/db": true,
		"internal/ledger":          true,
		"internal/masters":         true,
		"internal/reporting":       true,
		"internal/identity":        true,
		"internal/compliance":      true,
		"internal/compliance/gstr": true,
		"internal/inventory":       true,
		"internal/integration":     true,
		"internal/migration":       true,
	},
	"internal/archtest": {},
}

// backendRoot walks up from the test's working directory to the module root
// (the directory containing go.mod).
func backendRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not locate go.mod (backend root)")
	return ""
}

func TestInternalImportBoundaries(t *testing.T) {
	root := backendRoot(t)
	internalRoot := filepath.Join(root, "internal")

	fset := token.NewFileSet()
	violations := 0

	err := filepath.Walk(internalRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		// Skip test files: tests legitimately import siblings to exercise them.
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}

		// Determine which module this file belongs to (its package dir,
		// relative to backend root, using forward slashes).
		rel, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		pkg := filepath.ToSlash(rel)

		allowed, known := allowedDeps[pkg]
		if !known {
			t.Errorf("package %q is not declared in allowedDeps; add its boundary policy", pkg)
			return nil
		}

		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			ip := strings.Trim(imp.Path.Value, `"`)
			if !strings.HasPrefix(ip, modulePath+"/internal/") {
				continue // external or non-internal import: not our concern
			}
			dep := strings.TrimPrefix(ip, modulePath+"/")
			if dep == pkg {
				continue
			}
			if !allowed[dep] {
				t.Errorf("forbidden import: %q -> %q (not in allowed set)", pkg, dep)
				violations++
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal: %v", err)
	}
	if violations == 0 {
		t.Log("import boundaries clean")
	}
}
