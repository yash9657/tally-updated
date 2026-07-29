package payrollfile

import (
	"bytes"
	"encoding/csv"
	"errors"
	"strings"
	"testing"
	"time"
)

func esiSample(t *testing.T) ESIRow {
	t.Helper()
	return ESIRow{
		IPNumber:          "3100123456",
		IPName:            "SUNITA DEVI",
		DaysWagesPaid:     dec(t, "26"),
		TotalMonthlyWages: dec(t, "18000"),
	}
}

// readESI parses the emitted CSV back, so the assertions are about what a
// reader of the file sees rather than about the bytes we happened to build.
func readESI(t *testing.T, rows []ESIRow) [][]string {
	t.Helper()
	var buf bytes.Buffer
	if err := WriteESIReturn(&buf, rows); err != nil {
		t.Fatalf("WriteESIReturn: %v", err)
	}
	recs, err := csv.NewReader(strings.NewReader(buf.String())).ReadAll()
	if err != nil {
		t.Fatalf("re-read emitted CSV: %v", err)
	}
	return recs
}

// TestESIReturn_HeadersAreThePortalsVerbatim pins the column headers. They are
// data, not a caption: the operator pastes these columns into the official ESIC
// template and a renamed or reordered column lands wages in the days field.
func TestESIReturn_HeadersAreThePortalsVerbatim(t *testing.T) {
	recs := readESI(t, []ESIRow{esiSample(t)})
	want := []string{
		"IP Number",
		"IP Name",
		"No. of Days for which wages paid",
		"Total Monthly Wages",
		"Reason code for 0 wages",
		"Last Working Day",
	}
	if len(recs) < 1 {
		t.Fatal("no header row")
	}
	if strings.Join(recs[0], "|") != strings.Join(want, "|") {
		t.Fatalf("headers changed:\n got %q\nwant %q", recs[0], want)
	}
}

// TestESIReturn_DaysRoundUp pins the direction of the day rounding.
//
// The portal's day count is a WHOLE number, and the fraction rounds UP, not to
// nearest: a person who worked any part of a day is insured for that day. The
// direction is not cosmetic — the count feeds the contribution record that
// decides whether the insured person has enough days in the contribution period
// to claim sickness benefit, so rounding down (or to nearest) shortens somebody's
// eligibility by a day at a time, always against the employee.
func TestESIReturn_DaysRoundUp(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"26", "26"},     // already whole: unchanged
		{"26.5", "27"},   // the half — round-to-nearest would also give 27
		{"26.2", "27"},   // round-to-nearest would give 26: THIS is the discriminating case
		{"0.1", "1"},     // any part of a day is a day
		{"30.999", "31"}, // and the ceiling is still bounded by the month
	}
	for _, tc := range cases {
		row := esiSample(t)
		row.DaysWagesPaid = dec(t, tc.in)
		recs := readESI(t, []ESIRow{row})
		got := recs[1][2]
		if got != tc.want {
			t.Errorf("days %s -> %q, want %q (fractions round UP; rounding down shortens the insured person's benefit eligibility)", tc.in, got, tc.want)
		}
		if strings.Contains(got, ".") {
			t.Errorf("days %s -> %q is not a whole number", tc.in, got)
		}
	}

	// Out of range in either direction is refused rather than clamped.
	for _, bad := range []string{"-0.5", "31.5", "40"} {
		row := esiSample(t)
		row.DaysWagesPaid = dec(t, bad)
		var buf bytes.Buffer
		if err := WriteESIReturn(&buf, []ESIRow{row}); !errors.Is(err, ErrInvalidESIDays) {
			t.Errorf("days %s: got %v, want ErrInvalidESIDays", bad, err)
		}
	}
}

// TestESIReturn_ReasonCodeVocabularyIsClosed pins the closed vocabulary.
//
// The portal has a dropdown. A value outside it is not "a slightly unusual
// reason", it is a row the portal cannot accept — and silently blanking an
// unrecognised reason is worse than refusing it, because a blank reason against
// zero wages is itself a rejection that looks like our output was accepted but
// empty. A not-yet-implemented member must ERROR, not quietly work.
func TestESIReturn_ReasonCodeVocabularyIsClosed(t *testing.T) {
	lwd := time.Date(2026, time.July, 18, 0, 0, 0, 0, time.UTC)

	// Every member of the vocabulary is accepted and emitted verbatim.
	accepted := []string{
		ESIReasonLeftService,
		ESIReasonRetired,
		ESIReasonOutOfCoverage,
		ESIReasonExpired,
		ESIReasonNonImplemented,
		ESIReasonRetrenchment,
	}
	if len(accepted) != len(esiReasons) {
		t.Fatalf("the vocabulary has %d members but this test exercises %d; a member added without a test is a member nobody checked", len(esiReasons), len(accepted))
	}
	for _, r := range accepted {
		row := esiSample(t)
		row.Reason = r
		if esiReasonNeedsLastWorkingDay[r] {
			row.LastWorkingDay = lwd
		}
		recs := readESI(t, []ESIRow{row})
		if got := recs[1][4]; got != r {
			t.Errorf("reason %q was emitted as %q", r, got)
		}
	}

	// Plausible-but-wrong values are REFUSED, not blanked. These are exactly what
	// somebody types: the right idea in the wrong words.
	for _, bad := range []string{
		"Resigned",
		"Left Service",              // wrong casing/wording of a real member
		"employee has left service", // wrong case
		"Absconding",
		"1", // the numeric code the portal shows beside the label
	} {
		row := esiSample(t)
		row.Reason = bad
		row.LastWorkingDay = lwd
		var buf bytes.Buffer
		err := WriteESIReturn(&buf, []ESIRow{row})
		if !errors.Is(err, ErrUnknownESIReason) {
			t.Errorf("reason %q: got %v, want ErrUnknownESIReason", bad, err)
		}
		if buf.Len() != 0 && strings.Contains(buf.String(), bad) {
			t.Errorf("reason %q leaked into the output despite the error", bad)
		}
	}
}

// TestESIReturn_ReasonAndLastWorkingDayAgree pins the two directions of the
// pairing, each of which is a silent wrong in the employee's disfavour:
// a termination with no last working day leaves the person on the employer's
// ESIC roll, and a last working day with no reason is a row the portal cannot
// interpret. Zero paid days with no reason at all is the case the column
// "Reason code for 0 wages" exists for.
func TestESIReturn_ReasonAndLastWorkingDayAgree(t *testing.T) {
	lwd := time.Date(2026, time.July, 18, 0, 0, 0, 0, time.UTC)

	row := esiSample(t)
	row.Reason = ESIReasonLeftService // a termination
	var buf bytes.Buffer
	if err := WriteESIReturn(&buf, []ESIRow{row}); !errors.Is(err, ErrMissingLastWorkingDay) {
		t.Errorf("termination without a last working day: got %v, want ErrMissingLastWorkingDay", err)
	}

	row = esiSample(t)
	row.LastWorkingDay = lwd
	if err := WriteESIReturn(&buf, []ESIRow{row}); !errors.Is(err, ErrLastWorkingDayWithoutReason) {
		t.Errorf("last working day without a reason: got %v, want ErrLastWorkingDayWithoutReason", err)
	}

	row = esiSample(t)
	row.DaysWagesPaid = dec(t, "0")
	row.TotalMonthlyWages = dec(t, "0")
	if err := WriteESIReturn(&buf, []ESIRow{row}); !errors.Is(err, ErrZeroWagesNeedsReason) {
		t.Errorf("zero paid days with no reason: got %v, want ErrZeroWagesNeedsReason", err)
	}

	// A coverage state carries no last working day and that is fine.
	row = esiSample(t)
	row.DaysWagesPaid = dec(t, "0")
	row.TotalMonthlyWages = dec(t, "0")
	row.Reason = ESIReasonOutOfCoverage
	recs := readESI(t, []ESIRow{row})
	if recs[1][5] != "" {
		t.Errorf("last working day = %q, want blank for a coverage state", recs[1][5])
	}

	// And a termination emits the date in the portal's DD/MM/YYYY.
	row = esiSample(t)
	row.Reason = ESIReasonRetired
	row.LastWorkingDay = lwd
	recs = readESI(t, []ESIRow{row})
	if got, want := recs[1][5], "18/07/2026"; got != want {
		t.Errorf("last working day = %q, want %q", got, want)
	}
}

// TestESIReturn_RefusesFractionalWages pins the deliberate asymmetry with the
// ECR: this writer does NOT round money. The ESI wage is the base the portal
// derives both contributions from, so a file emitter that rounded it would be
// silently changing the base of somebody's statutory contribution — a decision
// that belongs to the engine, under Rule 51, not to a CSV writer.
func TestESIReturn_RefusesFractionalWages(t *testing.T) {
	row := esiSample(t)
	row.TotalMonthlyWages = dec(t, "18000.50")
	var buf bytes.Buffer
	if err := WriteESIReturn(&buf, []ESIRow{row}); !errors.Is(err, ErrFractionalWages) {
		t.Fatalf("got %v, want ErrFractionalWages", err)
	}
	row.TotalMonthlyWages = dec(t, "-1")
	if err := WriteESIReturn(&buf, []ESIRow{row}); !errors.Is(err, ErrNegativeAmount) {
		t.Fatalf("negative wages: got %v, want ErrNegativeAmount", err)
	}
}

// TestESIReturn_RefusesMalformedIPNumber and the empty file: an IP number that
// is not 10 digits belongs to nobody, and an empty return is a nil filing.
func TestESIReturn_RefusesMalformedIPNumber(t *testing.T) {
	for _, bad := range []string{"", "310012345", "31001234567", "31001234XY"} {
		row := esiSample(t)
		row.IPNumber = bad
		var buf bytes.Buffer
		if err := WriteESIReturn(&buf, []ESIRow{row}); !errors.Is(err, ErrInvalidIPNumber) {
			t.Errorf("IP number %q: got %v, want ErrInvalidIPNumber", bad, err)
		}
	}
	var buf bytes.Buffer
	if err := WriteESIReturn(&buf, nil); err == nil {
		t.Error("an ESI return with no rows was written; that is a nil contribution filed by accident")
	}
}

// TestESIReturn_NameWithACommaSurvivesTheRoundTrip: CSV quoting is the only
// escaping this format has, and Indian names routinely carry commas in the
// "SURNAME, GIVEN" order some masters use. A name that broke the column
// alignment would shift wages into the reason column.
func TestESIReturn_NameWithACommaSurvivesTheRoundTrip(t *testing.T) {
	row := esiSample(t)
	row.IPName = `DEVI, SUNITA "BABLI"`
	recs := readESI(t, []ESIRow{row})
	if got := recs[1][1]; got != row.IPName {
		t.Fatalf("name round-tripped as %q, want %q", got, row.IPName)
	}
	if got := recs[1][3]; got != "18000" {
		t.Fatalf("wages column = %q after a comma in the name; the columns shifted", got)
	}
}
