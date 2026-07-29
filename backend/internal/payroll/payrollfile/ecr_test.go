package payrollfile

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

// dec is a test-local parser. It PANICS on a bad literal rather than returning
// an error, which is what we want in a fixture: a typo in a test's money
// literal must not silently become zero.
func dec(t *testing.T, s string) decimal.Decimal {
	t.Helper()
	d, err := decimal.NewFromString(s)
	if err != nil {
		t.Fatalf("bad decimal literal %q: %v", s, err)
	}
	return d
}

// ceilingMember is the ordinary case: a member whose wages exceed the statutory
// PF ceiling, with PF computed on the ceiling. Employee EPF 12% of 15,000 =
// 1,800; EPS 8.33% of 15,000 = 1,249.50 which the engine rounds to 1,250 under
// the EPFO convention; employer EPF share 1,800, so the "EPF & EPS Diff" is
// 1,800 - 1,250 = 550.
func ceilingMember(t *testing.T) ECRRow {
	t.Helper()
	return ECRRow{
		UAN:                     "100123456789",
		MemberName:              "RAVI KUMAR",
		GrossWages:              dec(t, "22000"),
		EPFWages:                dec(t, "15000"),
		EPSWages:                dec(t, "15000"),
		EDLIWages:               dec(t, "15000"),
		EmployeeEPFContribution: dec(t, "1800"),
		EmployerEPFContribution: dec(t, "1800"),
		EPSContribution:         dec(t, "1250"),
		NCPDays:                 0,
		RefundOfAdvances:        dec(t, "0"),
	}
}

// TestECR_ElevenFieldsInOrderWithHashTildeHash pins the line BYTE FOR BYTE.
//
// It is a golden-string test on purpose. The ECR has no header record, no field
// names and no schema: the ONLY thing that says field 5 is EPS Wages is its
// position between EPF Wages and EDLI Wages. A reordering, an added field or a
// changed delimiter is undetectable by any structural assertion and is a silent
// mis-filing of every member in the file — the portal would happily read the
// EDLI wage as the EPS wage and short the member's pension.
func TestECR_ElevenFieldsInOrderWithHashTildeHash(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteECR(&buf, []ECRRow{ceilingMember(t)}); err != nil {
		t.Fatalf("WriteECR: %v", err)
	}
	const want = "100123456789#~#RAVI KUMAR#~#22000#~#15000#~#15000#~#15000#~#1800#~#1250#~#550#~#0#~#0\n"
	if got := buf.String(); got != want {
		t.Fatalf("ECR line changed; every field is positional and a shift mis-files every member.\n got: %q\nwant: %q", got, want)
	}

	// And the field COUNT, independently of the golden string, so that a future
	// edit to `want` cannot quietly change the arity too.
	fields := strings.Split(strings.TrimSuffix(buf.String(), "\n"), ecrDelimiter)
	if len(fields) != ecrFieldCount {
		t.Fatalf("ECR line has %d fields, want exactly %d (EPFO rejects any other count)", len(fields), ecrFieldCount)
	}
	for i, f := range fields {
		if f != strings.TrimSpace(f) {
			t.Errorf("field %d is padded (%q); the ECR is variable width and padding is filed as part of the value", i+1, f)
		}
	}
}

// TestECR_DiffIsRoundedComponentsSubtracted is the one-rupee case.
//
// Field 9 ("EPF & EPS Diff Remitted") must be the SUBTRACTION OF THE TWO
// ALREADY-ROUNDED COMPONENTS, not the difference computed at full precision and
// rounded once. This fixture is chosen so the two answers DIFFER:
//
//	employer EPF 1000.40 -> rounds to 1000
//	EPS           500.60 -> rounds to  501
//	correct diff  1000 - 501            =  499
//	wrong  diff   round(1000.40-500.60) = round(499.80) = 500
//
// The wrong answer is the "more accurate" one, which is exactly why someone
// writes it. It is still wrong: field 9 has to reconcile against fields 4 and 8
// AS PRINTED IN THIS FILE — the Unified Portal recomputes the employer share as
// field 8 + field 9 and checks it against 12% of field 4 — so the single
// rounding produces a file that is one rupee out and is REJECTED ON UPLOAD, and
// the employer misses the remittance deadline chasing it.
func TestECR_DiffIsRoundedComponentsSubtracted(t *testing.T) {
	row := ceilingMember(t)
	row.EmployerEPFContribution = dec(t, "1000.40")
	row.EPSContribution = dec(t, "500.60")
	row.EmployeeEPFContribution = dec(t, "1000.40")

	var buf bytes.Buffer
	if err := WriteECR(&buf, []ECRRow{row}); err != nil {
		t.Fatalf("WriteECR: %v", err)
	}
	fields := strings.Split(strings.TrimSuffix(buf.String(), "\n"), ecrDelimiter)
	if len(fields) != ecrFieldCount {
		t.Fatalf("field count %d", len(fields))
	}
	const (
		wantEPF  = "1000" // field 7, employee share, rounded
		wantEPS  = "501"  // field 8, rounded
		wantDiff = "499"  // field 9 = 1000 - 501
		naive    = "500"  // round(1000.40 - 500.60)
	)
	if fields[6] != wantEPF {
		t.Errorf("field 7 (EPF contribution remitted) = %q, want %q", fields[6], wantEPF)
	}
	if fields[7] != wantEPS {
		t.Errorf("field 8 (EPS contribution remitted) = %q, want %q", fields[7], wantEPS)
	}
	if fields[8] == naive {
		t.Fatalf("field 9 (EPF-EPS diff) = %q: the difference was rounded ONCE instead of subtracting the two already-rounded components. Field 8 + field 9 = %s + %s no longer reconciles against the printed EPF wages, and the EPFO Unified Portal REJECTS the upload — the employer misses the remittance deadline.", fields[8], fields[7], fields[8])
	}
	if fields[8] != wantDiff {
		t.Fatalf("field 9 (EPF-EPS diff) = %q, want %q (= field 7's rounded employer share %s minus field 8 %s)", fields[8], wantDiff, wantEPF, wantEPS)
	}
}

// TestECR_ThreeWageColumnsAreNotCollapsed pins that EPF, EPS and EDLI wages are
// three independent columns.
//
// They diverge in ordinary cases and the divergence is the whole point of
// having three columns: here the employer pays PF on ACTUAL wages (EPF wages
// 22,000) while EPS and EDLI stay capped at the statutory ceiling (15,000), and
// the member is a new joiner excluded from EPS so the EPS wage is zero while
// EDLI wage is not. Collapsing any pair — the tempting "they're all the PF
// wage" simplification — mis-states the pension liability for the rest of that
// member's working life, and EDLI is the cover the family claims on a death in
// service.
func TestECR_ThreeWageColumnsAreNotCollapsed(t *testing.T) {
	row := ceilingMember(t)
	row.GrossWages = dec(t, "30000")
	row.EPFWages = dec(t, "22000")
	row.EPSWages = dec(t, "0")
	row.EDLIWages = dec(t, "15000")
	row.EmployeeEPFContribution = dec(t, "2640")
	row.EmployerEPFContribution = dec(t, "2640")
	row.EPSContribution = dec(t, "0")

	var buf bytes.Buffer
	if err := WriteECR(&buf, []ECRRow{row}); err != nil {
		t.Fatalf("WriteECR: %v", err)
	}
	fields := strings.Split(strings.TrimSuffix(buf.String(), "\n"), ecrDelimiter)
	gotEPF, gotEPS, gotEDLI := fields[3], fields[4], fields[5]
	if gotEPF != "22000" || gotEPS != "0" || gotEDLI != "15000" {
		t.Fatalf("wage columns collapsed: EPF=%q EPS=%q EDLI=%q, want 22000 / 0 / 15000 — three independent statutory bases", gotEPF, gotEPS, gotEDLI)
	}
	if gotEPF == gotEPS || gotEPS == gotEDLI {
		t.Fatalf("two of the three wage columns are equal (%q, %q, %q); this fixture no longer demonstrates that they are independent", gotEPF, gotEPS, gotEDLI)
	}
}

// TestECR_BlankUANIsAnError pins that a missing UAN refuses the whole file.
//
// Under ECR 2.0 the UAN is mandatory and there is no "unknown" convention. A
// blank would be uploaded as a member with no identity: either the file is
// rejected outright, or the contribution is credited to nobody and the member's
// passbook simply never shows the month.
func TestECR_BlankUANIsAnError(t *testing.T) {
	for _, bad := range []string{"", "   ", "10012345678", "1001234567890", "10012345678X"} {
		row := ceilingMember(t)
		row.UAN = bad
		var buf bytes.Buffer
		err := WriteECR(&buf, []ECRRow{row})
		if err == nil {
			t.Fatalf("UAN %q was accepted; a member with no valid UAN is credited to nobody", bad)
		}
		if strings.TrimSpace(bad) == "" && !errors.Is(err, ErrBlankUAN) {
			t.Errorf("UAN %q: got %v, want ErrBlankUAN", bad, err)
		}
		if strings.TrimSpace(bad) != "" && !errors.Is(err, ErrInvalidUAN) {
			t.Errorf("UAN %q: got %v, want ErrInvalidUAN", bad, err)
		}
		if buf.Len() != 0 {
			t.Errorf("UAN %q: %d bytes were written despite the error; a partial ECR would be uploaded", bad, buf.Len())
		}
	}
}

// TestECR_RefusesValuesItCannotFile covers the remaining refusals: a negative
// statutory amount (never a correction mechanism in this format — corrections
// are a separate return, so a negative here is always an upstream bug), a name
// carrying the delimiter (the format has no escape syntax, so the line would
// silently gain a field and every later field would shift), an out-of-range NCP
// day count, and EPS exceeding the employer share (which would print a negative
// field 9).
func TestECR_RefusesValuesItCannotFile(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*ECRRow)
		wantErr error
	}{
		{"negative gross", func(r *ECRRow) { r.GrossWages = dec(t, "-1") }, ErrNegativeAmount},
		{"negative refund", func(r *ECRRow) { r.RefundOfAdvances = dec(t, "-0.01") }, ErrNegativeAmount},
		{"delimiter in name", func(r *ECRRow) { r.MemberName = "RAVI#~#KUMAR" }, ErrDelimiterInField},
		{"newline in name", func(r *ECRRow) { r.MemberName = "RAVI\nKUMAR" }, ErrDelimiterInField},
		{"ncp days negative", func(r *ECRRow) { r.NCPDays = -1 }, ErrInvalidNCPDays},
		{"ncp days 32", func(r *ECRRow) { r.NCPDays = 32 }, ErrInvalidNCPDays},
		{"eps above employer share", func(r *ECRRow) { r.EPSContribution = dec(t, "1801") }, ErrEPSExceedsEmployerEPF},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := ceilingMember(t)
			tc.mutate(&row)
			var buf bytes.Buffer
			err := WriteECR(&buf, []ECRRow{row})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("got %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// TestECR_EmptyFileIsRefused: an ECR with no members is a NIL return for the
// month, which is a different filing with different consequences. Emitting an
// empty file lets an operator believe they filed.
func TestECR_EmptyFileIsRefused(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteECR(&buf, nil); err == nil {
		t.Fatal("an ECR with no members was written; that is a nil return filed by accident")
	}
}
