package bankstatement

import (
	"strings"
	"testing"
)

// name_test.go — D111: A STATEMENT THAT ARRIVED WITHOUT A FILE NAME STILL HAS
// TO BE NAMEABLE, BECAUSE A DISCLOSURE NAMES IT.
//
// ⛔ THE MEASUREMENT (2026-08-31). Two statements PASTED into the import panel
// (rather than chosen with the file picker) were both stored with
// `file_name = ''`, confirmed in psql. The combined "everything imported for
// this account" report then rendered its per-file disclosure as:
//
//	· "" carried no running balance, so its rows were NOT balance-walked: a row
//	  dropped or duplicated by the export would not have been detected.
//
// That sentence's WHOLE JOB is to say WHICH file was not balance-checked, and
// with two imports on one account it named neither.
//
// ⛔ THE NAME IS DERIVED HERE, IN THE WRITER, AND NOT IN THE PANEL. The panel
// already had a fallback — `pasted.csv` — applied to the PREVIEW call and not to
// the import, ninety-nine lines apart in the same component. Copying it across
// would have made every paste indistinguishable from every other paste, which is
// the same defect one step over. The server is the one place that knows the
// facts that DO distinguish them, and it names the file for every client rather
// than for the one that was fixed.
//
// ⚠️ THE PASTE PATH IS NOT AN EDGE CASE. Its own comment in the panel calls it
// "an ordinary Tuesday": a bank portal that will not download, or a file on
// another machine.

func TestPastedStatementNameCarriesPeriodRowsAndFingerprint(t *testing.T) {
	got := pastedStatementName("2026-04-01", "2026-04-30", 42,
		"3f9a1c7ec0ffee00deadbeef00000000000000000000000000000000000000ff")
	want := "pasted statement 2026-04-01 to 2026-04-30, 42 rows (3f9a1c7e)"
	if got != want {
		t.Fatalf("name = %q, want %q", got, want)
	}
}

// TWO PASTES ON ONE ACCOUNT MUST NOT SHARE A NAME. This is the defect itself,
// stated as a property: same period, same row count, different bytes.
func TestTwoDifferentPastesGetDifferentNames(t *testing.T) {
	a := pastedStatementName("2026-04-01", "2026-04-30", 42, strings.Repeat("a", 64))
	b := pastedStatementName("2026-04-01", "2026-04-30", 42, strings.Repeat("b", 64))
	if a == b {
		t.Fatalf("two different files share the name %q — which is the defect, "+
			"one step over: a fixed fallback like %q names neither of them", a, "pasted.csv")
	}
}

// A ONE-DAY STATEMENT DOES NOT SAY "2026-04-01 to 2026-04-01", and one row is
// not "1 rows". A name a person reads is a name that has to read like one.
func TestPastedStatementNameOfASingleDayAndASingleRow(t *testing.T) {
	got := pastedStatementName("2026-04-01", "2026-04-01", 1, strings.Repeat("c", 64))
	want := "pasted statement 2026-04-01, 1 row (cccccccc)"
	if got != want {
		t.Fatalf("name = %q, want %q", got, want)
	}
}

// ⛔ A CHOSEN FILE KEEPS ITS OWN NAME. The control: this must be the fallback
// for an ABSENT name and nothing else, or an operator's "HDFC-april.csv" would
// be renamed by the server underneath them.
func TestResolveFileNameOnlyFillsInAnAbsentOne(t *testing.T) {
	hash := strings.Repeat("d", 64)
	if got := resolveFileName("HDFC-april.csv", "2026-04-01", "2026-04-30", 42, hash); got != "HDFC-april.csv" {
		t.Fatalf("a chosen file was renamed to %q", got)
	}
	// Whitespace is not a name: it renders as blank in exactly the sentence
	// this defect is about.
	if got := resolveFileName("   ", "2026-04-01", "2026-04-30", 42, hash); !strings.HasPrefix(got, "pasted statement ") {
		t.Fatalf("a whitespace-only name survived as %q", got)
	}
	if got := resolveFileName("", "2026-04-01", "2026-04-30", 42, hash); !strings.HasPrefix(got, "pasted statement ") {
		t.Fatalf("an empty name survived as %q", got)
	}
	// ⚠️ AND A CHOSEN NAME IS TRIMMED, not left with the whitespace a paste
	// into a file-name field carries.
	if got := resolveFileName("  HDFC-april.csv \n", "2026-04-01", "2026-04-30", 42, hash); got != "HDFC-april.csv" {
		t.Fatalf("a chosen file name was not trimmed: %q", got)
	}
}
