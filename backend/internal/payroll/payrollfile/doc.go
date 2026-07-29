// Package payrollfile emits the three statutory payroll FILES: the EPFO
// Electronic Challan-cum-Return (ECR), the income-tax Form 138 (ex-24Q)
// Annexure I quarterly statement, and the ESIC monthly contribution return.
//
// ---------------------------------------------------------------------------
// NOT VALIDATOR-VERIFIED — READ THIS FIRST, AND BEFORE ANY REAL FILING
// ---------------------------------------------------------------------------
// This output has NOT been checked by the NSDL/Protean FVU, the EPFO Unified
// Portal or the ESIC employer portal. None of them can run in this environment
// (Java desktop utilities, and this machine has no network). The golden corpus
// is a substitute for validation, not evidence of it.
//
// Every field name, delimiter, record order and length rule below was curated
// from SECONDARY sources with a model training cutoff, not read out of the
// primary specification: the official ITD "File Format for Form 24Q" data
// structure returned HTTP 403 from this machine and the legacy v7.2 .xls could
// not be opened here. Re-verify against the primary documents, and run the real
// validators, before a single file produced by this package is uploaded.
//
// ---------------------------------------------------------------------------
// WHERE THE DISCLOSURE LIVES, AND WHY IT IS NOT INSIDE THE FILES
// ---------------------------------------------------------------------------
// The brief for this package asked for the disclosure to be repeated "in each
// generated file's header where the format permits a comment". NONE OF THE
// THREE FORMATS PERMITS ONE, and this is a finding rather than an omission:
//
//   - The ECR is a bare `#~#`-delimited member list with no header record and
//     no comment syntax. The Unified Portal parses EVERY line as a member, so a
//     leading comment line is a malformed member and rejects the upload.
//   - The Form 138 file is a caret-delimited record stream whose first record
//     MUST be the File Header (FH); the FVU reads record type from field 2 of
//     line 1. A comment line makes the file unreadable.
//   - The ESI return is pasted into the official ESIC template, whose first row
//     must be the column headers. An extra row shifts every subsequent row.
//
// Writing the disclosure into a free-text statutory field (deductor branch,
// member name, remarks) was rejected outright: that is corrupting filed data to
// carry a comment. Instead the disclosure is exported as [Disclosure] and
// [WriteDisclosureSidecar] writes it as a companion .README.txt next to the
// generated file. The caller is expected to emit the sidecar every time it
// emits a statutory file.
//
// ---------------------------------------------------------------------------
// WHAT IS BUILT
// ---------------------------------------------------------------------------
//   - PF ECR ([WriteECR]): plain .txt, `#~#`-delimited, the 11 fields in the
//     EPFO ECR 2.0 order, all money in WHOLE RUPEES. EPF Wages, EPS Wages and
//     EDLI Wages are THREE DIFFERENT NUMBERS and are never collapsed. Admin and
//     inspection charges are correctly ABSENT — the Unified Portal computes them
//     on upload from the wages in the file, so a file that carried them would be
//     double-counting.
//   - Form 138 (ex-24Q) ANNEXURE I, Q1-Q3 ONLY ([WriteForm138AnnexureI]):
//     caret-delimited variable-width ASCII, record types FH -> BH -> CD -> DD,
//     one record per line, the BH carrying the counts of the records that
//     follow.
//   - ESI monthly contribution return ([WriteESIReturn]): CSV with the ESIC
//     column headers, days a whole number rounded UP, the leaving reason drawn
//     from a closed vocabulary.
//
// ---------------------------------------------------------------------------
// DOCUMENTED GAPS — each deliberate, none an oversight
// ---------------------------------------------------------------------------
//   - FORM 138 Q4 / ANNEXURE II IS NOT BUILT. The Q4 format is NOT PUBLISHED:
//     Protean lists the Q1-Q3 structure only and describes the Q4 structure as
//     "expected soon". Building it against the legacy 24Q v7.2 layout would be
//     guessing at the salary-detail (Annexure II) record that carries every
//     employee's whole-year taxable income — the single most consequential
//     record in payroll. [WriteForm138AnnexureI] therefore REFUSES a Q4 file
//     with [ErrQ4FormatNotPublished] rather than emitting a guess.
//   - THE ESI RETURN IS CSV, NOT THE EXCEL 97-2003 (BIFF8) WORKBOOK the portal
//     reportedly requires (it is said to reject .xlsx). Emitting BIFF8 from Go
//     needs a new third-party dependency, which is a decision outside this
//     track's mandate. The user pastes this CSV into the official ESIC template.
//     WE DO NOT CLAIM PORTAL-ACCEPTABLE OUTPUT.
//   - EDLI CONTRIBUTION AND EDLI/EPF ADMIN CHARGES ARE ABSENT. The EDLI Scheme
//     2026 rate is expressly "to be notified separately", so the engine emits a
//     Deferral rather than a number and this package has no column for one.
//   - PROFESSIONAL TAX (state slab tables not researched), s.157 (ex-89) arrears
//     relief, gratuity, bonus, LWF and a bank advice file are NOT BUILT.
//
// ---------------------------------------------------------------------------
// WHY THIS PACKAGE IMPORTS NOTHING FROM internal/payroll
// ---------------------------------------------------------------------------
// Its archtest allowed-dependency set is the EMPTY MAP, deliberately, and each
// emitter takes its OWN file-local row struct (ECRRow, Form138Deductee, ESIRow)
// rather than a payroll.Payslip. internal/payroll/payrolldb does the mapping.
//
// The emptiness is the load-bearing part. If this package could import the
// engine, a file format's field list would start dictating engine semantics:
// the ECR's "EPF-EPS Diff Remitted" column would become a field on the PF
// result, the Form 138 DD record layout would leak into the income-tax result,
// and the day the Q4 format is finally published the engine would have to change
// to accommodate a file. Keeping the edge absent is what makes these writers
// replaceable. A test in this package (TestPayrollFileImportsNoInternalPackage)
// fails the build on any internal import, so the rule cannot erode by accident
// even if the archtest map is later loosened.
//
// PII: the row structs carry PAN, UAN, the ESIC IP number and employee names
// because the statutory outputs cannot be produced without them. NOTHING in
// this package (or in any payroll package) may log, print or hash them —
// TestPayrollPackagesDoNotLog scans all three payroll packages and fails on any
// import of "log"/"log/slog" or any fmt print to stdout/stderr.
package payrollfile

import (
	"fmt"
	"io"
	"strings"

	"github.com/shopspring/decimal"
)

// Disclosure is the not-validator-verified statement. It is exported so the
// caller can surface it in the UI beside the download button, and it is written
// verbatim into the sidecar by [WriteDisclosureSidecar].
//
// TestPayrollFileDisclosureIsPresent pins the load-bearing sentences of this
// text in doc.go's SOURCE, so softening the wording here fails the build. That
// test exists because the failure mode is social, not technical: the sentence
// gets trimmed to "not yet validated" by someone who finds it alarming, and the
// operator then believes the file was checked.
const Disclosure = `This output has NOT been checked by the NSDL/Protean FVU, the EPFO Unified
Portal or the ESIC employer portal. None of them can run in this environment
(Java desktop utilities, and this machine has no network). The golden corpus is
a substitute for validation, not evidence of it.

Every field name, delimiter, record order and length rule was curated from
SECONDARY sources, not read out of the primary specification. Re-verify against
the primary documents, and run the real validators, before uploading.

KNOWN GAPS in the files this software produces:
  * Form 138 Q4 / Annexure II is NOT PRODUCED: the format is not published.
  * The ESI return is a CSV to be pasted into the official ESIC template; it is
    NOT the Excel 97-2003 workbook the portal requires, and is not claimed to be
    portal-acceptable output.
  * EDLI contribution and EDLI/EPF admin charges are absent: the EDLI Scheme
    2026 rate is "to be notified separately".
  * Professional Tax, s.157 (ex-89) arrears relief, gratuity, bonus, LWF and a
    bank advice file are not produced at all.
`

// WriteDisclosureSidecar writes [Disclosure] as a companion README for a
// generated statutory file, naming the file it belongs to.
//
// It exists because none of the three formats permits an in-file comment (see
// the package doc). A caller that writes "ecr-202607.txt" should write
// "ecr-202607.txt.README.txt" beside it with this content; a caller that
// streams a download should attach it. The disclosure travelling with the file
// is the only mechanism available once the bytes leave this process.
func WriteDisclosureSidecar(w io.Writer, generatedFileName string) error {
	if generatedFileName == "" {
		return fmt.Errorf("payrollfile: disclosure sidecar needs the name of the file it describes")
	}
	_, err := fmt.Fprintf(w, "DISCLOSURE FOR: %s\n\n%s", generatedFileName, Disclosure)
	if err != nil {
		return fmt.Errorf("payrollfile: write disclosure sidecar: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Helpers shared by the three emitters.
//
// They are here, in doc.go, rather than in a helpers.go so that every rounding
// and every format decision in this package sits under the provenance banner
// above. The writers never round for themselves: integer-only statutory columns
// go through [rupees] (which rounds via [roundRupee]) and two-decimal columns go
// through [amount2]. Both refuse a negative before rounding it.
// ---------------------------------------------------------------------------

// roundRupee rounds to whole rupees, half away from zero.
//
// shopspring's Round is half-AWAY-FROM-ZERO, which coincides with the statutory
// half-UP only for non-negative values. Every caller in this package rejects a
// negative before rounding it (see [ErrNegativeAmount]), so the two rules never
// diverge on anything this package emits — and a negative that would have made
// them diverge is a computation bug we refuse to file rather than round.
func roundRupee(d decimal.Decimal) decimal.Decimal { return d.Round(0) }

// rupees validates and rounds one money field for an integer-only statutory
// column. `what` names the field for the error and is a compile-time constant
// at every call site, never data (see the note about PII in error strings).
func rupees(d decimal.Decimal, what string) (decimal.Decimal, error) {
	if d.IsNegative() {
		return decimal.Decimal{}, fmt.Errorf("%w: %s is %s", ErrNegativeAmount, what, d.String())
	}
	return roundRupee(d), nil
}

// amount2 validates and renders one money field for a two-decimal statutory
// column (the Form 138 amount fields). StringFixed rounds half away from zero
// at the paisa, the same rule [roundRupee] applies at the rupee, and the
// negative check above it means the two never diverge from the statutory
// half-up on anything this package emits.
func amount2(d decimal.Decimal, what string) (string, error) {
	if d.IsNegative() {
		return "", fmt.Errorf("%w: %s is %s", ErrNegativeAmount, what, d.String())
	}
	return d.StringFixed(2), nil
}

// isDigits reports whether s is exactly n ASCII digits. Used for the identifier
// formats (UAN 12, IP number 10) where a wrong length is a rejected upload.
func isDigits(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// rejectDelimiters refuses a text field that carries the format's delimiter or
// a line break. None of these three formats has an escape syntax, so escaping
// is not an option and silently stripping would alter a filed name; the only
// safe answer is to refuse and make the caller fix the master data.
func rejectDelimiters(s string, delim string) error {
	if delim != "" && strings.Contains(s, delim) {
		return fmt.Errorf("%w: contains %q", ErrDelimiterInField, delim)
	}
	if strings.ContainsAny(s, "\r\n") {
		return fmt.Errorf("%w: contains a line break", ErrDelimiterInField)
	}
	return nil
}
