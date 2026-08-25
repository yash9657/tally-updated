package migration_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/masters"
	"github.com/khata/cored/internal/migration"
	"github.com/khata/cored/internal/reporting"
)

// TestTallyPrimeShapesCorpus is the CI stand-in for the real-file gate. Every
// assertion below FAILED against a genuine TallyPrime export before the
// importer was taught the shape it asserts, and the fixture is byte-for-byte in
// the form Tally writes: UTF-16LE with a BOM, CRLF, &#4; inside ordinary field
// values, and the payload nested under IMPORTDATA/REQUESTDESC/REQUESTDATA.
//
// It is anonymised — invented parties, invented amounts — because the file it
// stands in for is a real company's ledger and cannot be committed. What it
// preserves is the SHAPE, which is the part that broke.
func TestTallyPrimeShapesCorpus(t *testing.T) {
	pool, ctx, companyID := setup(t)
	path := filepath.Join(repoRoot(t), "corpora", "migration", "06-tallyprime-real-shapes.xml")

	ms := masters.NewService(pool)
	im := migration.NewImporter(ms, ledger.NewService(pool))

	res, err := im.ImportFile(ctx, path, "tester")
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	// ---- the envelope was READ AT ALL. A file in this shape used to parse
	// cleanly and yield zero messages, so the import "succeeded" having done
	// nothing. Anything less than one voucher here is that failure returning.
	if res.SourceCompany != "Shapes Trading Co" {
		t.Errorf("source company %q — the REQUESTDESC under IMPORTDATA was not read",
			res.SourceCompany)
	}
	if res.VouchersPosted != 1 {
		t.Fatalf("vouchers posted = %d, want 1 (a UTF-16 envelope nested under IMPORTDATA "+
			"must not import silently empty)", res.VouchersPosted)
	}

	// ---- the user's own groups were created, Tally's built-ins were NOT
	// duplicated. "Cash-in-hand" in the file is the seeded "Cash-in-Hand".
	wantGroups := map[string]bool{"RETAIL DEBTORS": true, "BROKERAGE EXPS.": true}
	for _, g := range res.GroupsEnsured {
		if !wantGroups[g] {
			t.Errorf("group %q was CREATED; only the user-defined groups should be", g)
		}
		delete(wantGroups, g)
	}
	for g := range wantGroups {
		t.Errorf("user-defined group %q was not created; ledgers under it cannot exist", g)
	}
	if _, stored, err := ms.GroupIDByName(ctx, "Cash-in-hand"); err != nil {
		t.Errorf("resolve Cash-in-hand: %v", err)
	} else if stored != "Cash-in-Hand" {
		t.Errorf("Tally's %q resolved to %q — a SECOND cash group was created beside the seeded one",
			"Cash-in-hand", stored)
	}

	// ---- the item invoice balanced. Its revenue line exists only inside
	// ALLINVENTORYENTRIES.LIST > ACCOUNTINGALLOCATIONS.LIST, and its round-off
	// line says credit and means debit; either read wrongly and the double-entry
	// trigger refuses the post, so reaching this point at all is half the proof.
	rep := reporting.NewService(pool)
	tb, err := rep.TrialBalance(ctx, companyID, "")
	if err != nil {
		t.Fatalf("trial balance: %v", err)
	}
	if !tb.IsBalanced() {
		t.Fatalf("trial balance not balanced: dr=%s cr=%s", tb.TotalDebit, tb.TotalCredit)
	}

	net := map[string]decimal.Decimal{}
	for _, r := range tb.Rows {
		net[strings.ToUpper(strings.TrimSpace(r.LedgerName))] = r.Debit.Sub(r.Credit)
	}
	// Opening 100000/50000/12000 Dr against 162000 Cr capital, plus the invoice:
	// party Dr 10500, sales Cr 10000, brokerage Cr 500.40, round off Dr 0.40.
	for _, tc := range []struct {
		ledger string
		want   string
		why    string
	}{
		{"CASH", "100000.0000",
			"the opening balance of a ledger the company was ALREADY SEEDED with must still be applied"},
		{"KUMAR STORES", "60500.0000", "opening 50000 Dr plus the invoice's 10500 Dr"},
		{"SHAH TRADERS", "12000.0000",
			"an opening whose bill-wise breakup does not add up keeps its TOTAL"},
		{"CAPITAL A/C", "-162000.0000", "credit opening"},
		{"SALES-GRAIN", "-10000.0000",
			"revenue lives ONLY in the stock line's ACCOUNTINGALLOCATIONS on an item invoice"},
		{"BROKERAGE", "-500.4000", "a charge ledger on the invoice"},
		{"ROUND OFF", "0.4000",
			"ISDEEMEDPOSITIVE says credit and AMOUNT says debit; the SIGN is the authority"},
	} {
		got, ok := net[tc.ledger]
		if !ok {
			t.Errorf("ledger %q missing from the trial balance (%s)", tc.ledger, tc.why)
			continue
		}
		if !got.Equal(decimal.RequireFromString(tc.want)) {
			t.Errorf("ledger %q net = %s, want %s — %s",
				tc.ledger, got.StringFixed(4), tc.want, tc.why)
		}
	}

	// ---- the party's GST identity came from the DATED registration list, not
	// from the stale flat PARTYGSTIN, and its state code was derived from a file
	// that states the state only by NAME.
	lid, _, err := ms.LedgerIDByName(ctx, "KUMAR STORES")
	if err != nil {
		t.Fatalf("resolve KUMAR STORES: %v", err)
	}
	gst, err := ms.LedgerGSTInfo(ctx, lid)
	if err != nil {
		t.Fatalf("ledger gst info: %v", err)
	}
	if gst.GSTIN != "29AAAAA0000A1Z5" {
		t.Errorf("GSTIN = %q, want the CURRENT registration 29AAAAA0000A1Z5 — the flat "+
			"PARTYGSTIN still holds the superseded 27… one", gst.GSTIN)
	}
	if gst.StateCode != "29" {
		t.Errorf("state code = %q, want 29; the export names the state only as "+
			"\"Karnataka\" and carries no two-digit code anywhere", gst.StateCode)
	}

	// ---- what could not be represented exactly was REPORTED, not swallowed.
	var sawBreakupWarning bool
	for _, w := range res.Warnings {
		if strings.Contains(w, "SHAH TRADERS") && strings.Contains(w, "breakup") {
			sawBreakupWarning = true
		}
	}
	if !sawBreakupWarning {
		t.Errorf("an opening bill-wise breakup that sums to 10000 against a 12000 opening "+
			"was accepted silently; warnings were %v", res.Warnings)
	}
}
