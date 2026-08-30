package server

import (
	"testing"

	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/reporting"
)

// TestOnAccountRefNameAgreesAcrossTheWriteAndReadSides is the guard on D106's
// one shared string, and it lives here because here is the only place both
// halves are visible.
//
// ⛔ WHAT BREAKS IF THIS FAILS, AND WHY NOTHING ELSE WOULD CATCH IT.
// `ledger` stamps an explicit ON_ACCOUNT reference with its own name;
// `reporting.Outstandings` files the DERIVED residue — the part of a party's
// balance that names no bill — under its own. The report groups by
// (ledger_id, ref_name), so the two spellings must be one bucket. If they drift:
//
//   - a party that has BOTH an explicit on-account reference and an unreferenced
//     movement shows TWO rows, each internally correct, neither being the
//     party's on-account position;
//   - the grand total stays right, so nothing foots wrong and no other test
//     notices;
//   - and `OnAccountTotal` still reconciles, because it sums by the flag rather
//     than by the name — so even the report's own reconciliation identity holds
//     while the rows a human reads are wrong.
//
// That is the failure this file exists to prevent: correct arithmetic over a
// split bucket.
//
// ⚠️ THE CONSTANTS ARE DELIBERATELY NOT SHARED. `internal/archtest` permits
// `reporting` to import only `platform/db`, `inventory` and `compliance/form140`
// — `ledger` is not on that list — and `ledger`'s own tests import `reporting`,
// so a shared constant in either package would be both a policy breach and a
// compile-time import cycle. Duplication plus this assertion is the cheaper of
// the two honest options.
func TestOnAccountRefNameAgreesAcrossTheWriteAndReadSides(t *testing.T) {
	if ledger.OnAccountRefName != reporting.OnAccountRefName {
		t.Fatalf(
			"the on-account bucket is spelled two ways: ledger=%q reporting=%q\n"+
				"an explicit ON_ACCOUNT reference and the derived residue would land in "+
				"different rows, and the totals would still add up",
			ledger.OnAccountRefName, reporting.OnAccountRefName)
	}
	// The name is also what an operator reads on the row, so it is not free to
	// become an internal token: it must stay the words Tally uses.
	if ledger.OnAccountRefName != "On Account" {
		t.Fatalf("the on-account bucket is shown to the operator as a bill reference; "+
			"got %q, want %q", ledger.OnAccountRefName, "On Account")
	}
}
