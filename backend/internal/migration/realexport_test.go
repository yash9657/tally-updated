package migration_test

import (
	"context"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/integration"
	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/masters"
	"github.com/khata/cored/internal/migration"
	"github.com/khata/cored/internal/reporting"
)

// ---------------------------------------------------------------------------
// THE REAL-FILE GATE (T32).
//
// Everything else in this package runs against corpora WE wrote. This file runs
// against an export TALLYPRIME wrote, and the difference is the whole point: a
// hand-authored envelope proves the importer can read the shape an integrator
// would send it, and proves nothing at all about the shape the product it must
// replace actually emits. Five defects in this package were invisible until a
// genuine file arrived, and three of them were silent — the worst being an
// envelope that parsed without error and imported ZERO vouchers.
//
// ⛔ THE FILE ITSELF IS NOT IN THE REPOSITORY AND MUST NOT BE. A real export is
// a real company's books: 3,706 named counter-parties, their GSTINs, their
// addresses, what they owe. Committing one to obtain a test fixture would
// publish a third party's ledger. The test is therefore OPT-IN by path:
//
//	CORED_TALLY_MASTERS_XML=/path/to/Master.xml \
//	CORED_TALLY_VOUCHERS_XML=/path/to/Transactions.xml \
//	go test ./internal/migration/ -run RealTallyExport -v
//
// Anonymised derivatives of the SHAPES it exercises live in
// corpora/migration/06-tallyprime-real-shapes.xml, and those DO gate CI.
// ---------------------------------------------------------------------------

// realExportPaths returns the masters and vouchers paths, skipping the test when
// no real export has been pointed at.
func realExportPaths(t *testing.T) (string, string) {
	t.Helper()
	m, v := os.Getenv("CORED_TALLY_MASTERS_XML"), os.Getenv("CORED_TALLY_VOUCHERS_XML")
	if m == "" || v == "" {
		t.Skip("set CORED_TALLY_MASTERS_XML and CORED_TALLY_VOUCHERS_XML to run the real-export gate")
	}
	return m, v
}

// TestRealTallyExportImportsAndReconcilesToThePaisa is the commercial gate: a
// genuine TallyPrime export goes in, and the Trial Balance cored reports back
// must equal — exactly, ledger by ledger — a trial balance recomputed from the
// XML by plain arithmetic that shares no code with the importer.
//
// The oracle is deliberately independent: it re-reads the same two files, adds
// up every opening balance and every voucher line itself, and compares. It does
// not consult a JSON fixture, so it cannot be quietly restated to match a
// regression.
func TestRealTallyExportImportsAndReconcilesToThePaisa(t *testing.T) {
	mastersPath, vouchersPath := realExportPaths(t)
	pool, ctx, companyID := setup(t)

	im := migration.NewImporter(masters.NewService(pool), ledger.NewService(pool), pool)

	mres, err := im.ImportFile(ctx, mastersPath, "tester")
	if err != nil {
		t.Fatalf("import masters: %v", err)
	}
	t.Logf("masters: %d groups created, %d ledgers ensured, %d warnings",
		len(mres.GroupsEnsured), len(mres.LedgersEnsured), len(mres.Warnings))
	for _, w := range mres.Warnings {
		t.Logf("  warning: %s", w)
	}

	vres, err := im.ImportFile(ctx, vouchersPath, "tester")
	if err != nil {
		t.Fatalf("import vouchers: %v", err)
	}
	t.Logf("vouchers: %d posted, %d altered, %d deleted, %d skipped",
		vres.VouchersPosted, vres.VouchersAltered, vres.VouchersDeleted, vres.Skipped)
	for _, w := range vres.Warnings {
		t.Logf("  warning: %s", w)
	}

	// Every posted voucher went through the immutable hash-chained post path.
	for i, p := range vres.Posted {
		if p.VersionNo != 1 || len(p.Hash) == 0 {
			t.Fatalf("voucher %d: version %d hash %d bytes — not a first immutable version",
				i+1, p.VersionNo, len(p.Hash))
		}
	}

	want := expectedFromExport(t, mastersPath, vouchersPath)

	rep := reporting.NewService(pool)
	tb, err := rep.TrialBalance(ctx, companyID, "")
	if err != nil {
		t.Fatalf("trial balance: %v", err)
	}
	if !tb.IsBalanced() {
		t.Fatalf("trial balance is not balanced: dr=%s cr=%s", tb.TotalDebit, tb.TotalCredit)
	}

	// Keyed case-insensitively: cored stores the spelling it already had for a
	// ledger the export names in different case ("Salary Payable" vs the export's
	// "SALARY PAYABLE"), and that is the SAME ledger — it is what the normalised
	// uniqueness rule means.
	got := make(map[string]decimal.Decimal, len(tb.Rows))
	for _, r := range tb.Rows {
		got[strings.ToLower(strings.TrimSpace(r.LedgerName))] = r.Debit.Sub(r.Credit)
	}

	var names []string
	for n := range want {
		names = append(names, n)
	}
	sort.Strings(names)

	var mismatches int
	for _, n := range names {
		w := want[n]
		g, present := got[n]
		if !present {
			if w.IsZero() {
				continue // a ledger with no balance need not appear
			}
			t.Errorf("ledger %q missing from the trial balance (expected net %s)", n, w.StringFixed(4))
			mismatches++
			continue
		}
		if !g.Equal(w) {
			t.Errorf("ledger %q: trial balance net %s, recomputed from the export %s (off by %s)",
				n, g.StringFixed(4), w.StringFixed(4), g.Sub(w).StringFixed(4))
			mismatches++
		}
	}
	for n, g := range got {
		if _, ok := want[n]; !ok && !g.IsZero() {
			t.Errorf("ledger %q has net %s in the trial balance but nothing in the export",
				n, g.StringFixed(4))
			mismatches++
		}
	}
	if mismatches == 0 {
		t.Logf("RECONCILED: %d ledgers, Dr %s = Cr %s, to the paisa",
			len(want), tb.TotalDebit.StringFixed(2), tb.TotalCredit.StringFixed(2))
	}
}

// expectedFromExport recomputes the whole trial balance straight from the two
// XML files: opening balance per ledger, plus every voucher line. Net is
// debit-positive. It shares no arithmetic with the importer.
func expectedFromExport(t *testing.T, mastersPath, vouchersPath string) map[string]decimal.Decimal {
	t.Helper()
	net := make(map[string]decimal.Decimal)

	for _, m := range parseFile(t, mastersPath) {
		if m.Ledger == nil {
			continue
		}
		name := m.Ledger.Name()
		debit, ok, err := m.Ledger.OpeningSide()
		if err != nil {
			t.Fatalf("opening side for %q: %v", name, err)
		}
		name = strings.ToLower(strings.TrimSpace(name))
		if _, seen := net[name]; !seen {
			net[name] = decimal.Zero
		}
		if !ok {
			continue
		}
		mag, err := m.Ledger.OpeningMagnitude()
		if err != nil {
			t.Fatalf("opening magnitude for %q: %v", name, err)
		}
		if !debit {
			mag = mag.Neg()
		}
		net[name] = net[name].Add(mag)
	}

	for _, m := range parseFile(t, vouchersPath) {
		if m.Voucher == nil {
			continue
		}
		for _, e := range m.Voucher.Entries() {
			// Independent of Side()/Magnitude(): the AMOUNT sign IS the posting,
			// negative being a debit, so a debit-positive net is just the negated sum.
			amt, err := decimal.NewFromString(strings.TrimSpace(e.Amount))
			if err != nil {
				t.Fatalf("ledger %q: bad AMOUNT %q", e.LedgerName, e.Amount)
			}
			n := strings.ToLower(strings.TrimSpace(e.LedgerName))
			net[n] = net[n].Sub(amt)
		}
	}
	return net
}

func parseFile(t *testing.T, path string) []integration.TallyMessage {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	env, err := integration.ParseTallyEnvelope(f)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if len(env.Messages()) == 0 {
		t.Fatalf("%s parsed to ZERO messages", path)
	}
	return env.Messages()
}

var _ = context.Background
