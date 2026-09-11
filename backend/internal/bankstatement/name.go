package bankstatement

import (
	"fmt"
	"strings"
)

// name.go — NAMING A STATEMENT THAT ARRIVED WITHOUT A FILE NAME (D111).
//
// ⛔ WHY THE SERVER NAMES IT AND NOT THE CLIENT. `bank_statement_import.file_name`
// is what the "everything imported for this account" report quotes when it has to
// say WHICH file could not be balance-walked, and an empty one renders that
// disclosure as `"" carried no running balance…` — a sentence whose whole job is
// to name a file, naming none. The panel that pastes is one client; the name has
// to be right for every client, including a script, so it is filled in at the
// writer.
//
// ⛔⛔ AND THE NAME MUST DISTINGUISH TWO PASTES, WHICH IS THE WHOLE POINT. The
// obvious fallback — a constant like `pasted.csv` — reproduces the defect one
// step over: two pasted statements on one account become indistinguishable
// again, and the disclosure still names neither. So the name carries facts about
// the STATEMENT rather than about the moment it arrived:
//
//	pasted statement 2026-04-01 to 2026-04-30, 42 rows (3f9a1c7e)
//
//   - the PERIOD and the ROW COUNT, because those are what an operator matches
//     against the statement in their other window, and both come from the file's
//     own contents rather than from a clock (two pastes on one day are the
//     ordinary case, so a timestamp would not have separated them);
//   - the first eight characters of the file's SHA-256, which is the identity
//     this table already stores and already de-duplicates on. It makes the name
//     unique exactly when the FILE is different, and identical when the operator
//     pastes the same statement twice — which is the case the import treats as a
//     no-op success, so the two names agreeing is correct rather than a
//     collision.
//
// ⚠️ WHAT IT DOES NOT CLAIM. It is not the bank's own file name and does not
// pretend to be one: it opens with the word "pasted", so nobody reading the
// import list mistakes it for something that came off a download.

// resolveFileName returns the name to store for an import: the operator's own,
// trimmed, or a derived one when they supplied none.
//
// ⛔ IT FILLS IN AN ABSENCE AND NEVER OVERRIDES A NAME. An operator who chose
// "HDFC-april.csv" must find that string in the import list; a server that
// renamed it would be editing evidence.
func resolveFileName(supplied, periodFrom, periodTo string, lines int, fileSHA256 string) string {
	if name := strings.TrimSpace(supplied); name != "" {
		return name
	}
	return pastedStatementName(periodFrom, periodTo, lines, fileSHA256)
}

// pastedStatementName builds the derived name. Dates are ISO yyyy-mm-dd, as
// they are stored.
func pastedStatementName(periodFrom, periodTo string, lines int, fileSHA256 string) string {
	when := periodFrom
	// A one-day statement says one date. "2026-04-01 to 2026-04-01" reads as a
	// range somebody got wrong.
	if periodTo != periodFrom {
		when = periodFrom + " to " + periodTo
	}
	rows := "rows"
	if lines == 1 {
		rows = "row"
	}
	// The fingerprint is TRUNCATED for a name a person has to read and compare
	// by eye; the full hash is a column away in the same table for anyone who
	// needs to be certain. Short hashes are defended by `file_sha256`'s own
	// uniqueness check, not by this string.
	fingerprint := fileSHA256
	if len(fingerprint) > 8 {
		fingerprint = fingerprint[:8]
	}
	return fmt.Sprintf("pasted statement %s, %d %s (%s)", when, lines, rows, fingerprint)
}
