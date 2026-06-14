package ledger

import "testing"

// TestFinYearOf checks Indian FY (Apr–Mar) derivation, especially the boundary
// where 31-Mar belongs to the prior start-year and 1-Apr opens a new one.
func TestFinYearOf(t *testing.T) {
	cases := []struct {
		date string
		want int
	}{
		{"2026-03-31", 2025}, // last day of FY 2025-26
		{"2026-04-01", 2026}, // first day of FY 2026-27
		{"2026-07-15", 2026}, // mid FY 2026-27
		{"2027-02-01", 2026}, // still FY 2026-27 (Feb of next calendar year)
		{"2027-04-01", 2027}, // FY 2027-28
		{"2025-12-31", 2025}, // FY 2025-26
		{"2025-01-01", 2024}, // Jan => prior start-year
	}
	for _, c := range cases {
		got, err := finYearOf(c.date)
		if err != nil {
			t.Fatalf("finYearOf(%q): %v", c.date, err)
		}
		if got != c.want {
			t.Errorf("finYearOf(%q) = %d, want %d", c.date, got, c.want)
		}
	}

	if _, err := finYearOf("not-a-date"); err == nil {
		t.Error("expected error for malformed date")
	}
}

// TestFormatVoucherNo checks prefix/suffix and zero-padding (width nil => bare).
func TestFormatVoucherNo(t *testing.T) {
	w4 := 4
	w0 := 0
	cases := []struct {
		prefix, suffix string
		width          *int
		n              int64
		want           string
	}{
		{"INV/2026-27/", "", &w4, 42, "INV/2026-27/0042"},
		{"INV/", "/A", &w4, 1, "INV/0001/A"},
		{"", "", nil, 7, "7"},        // no padding
		{"", "", &w0, 7, "7"},        // width 0 => bare
		{"S", "", &w4, 12345, "S12345"}, // n wider than width => no truncation
	}
	for _, c := range cases {
		got := formatVoucherNo(c.prefix, c.suffix, c.width, c.n)
		if got != c.want {
			t.Errorf("formatVoucherNo(%q,%q,%v,%d) = %q, want %q",
				c.prefix, c.suffix, c.width, c.n, got, c.want)
		}
	}
}

// TestAllocatesAutomatically pins the method routing.
func TestAllocatesAutomatically(t *testing.T) {
	auto := []string{methodAutomatic, methodAutomaticManual, methodMultiUserAuto}
	for _, m := range auto {
		if !allocatesAutomatically(m) {
			t.Errorf("%q should allocate automatically", m)
		}
	}
	if allocatesAutomatically(methodManual) {
		t.Error("MANUAL must not allocate automatically")
	}
	if allocatesAutomatically("UNKNOWN") {
		t.Error("unknown method must not allocate automatically")
	}
}
