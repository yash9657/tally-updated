package masters

// THE PURE HALF OF THE CHALLAN WRITE PATH. NO DATABASE, ON PURPOSE.
//
// Everything in tds_challan.go that decides whether a deposit slip can be
// believed is a pure function of the input, and this file exercises it directly.
// That matters beyond convenience: the DB-backed suite next door
// (tds_challan_db_test.go) SKIPS when Postgres is unreachable, and a shape rule
// that only has a skippable test is a shape rule that is unverified on any
// machine where the database is down — which is exactly how a wrong BSR code
// reaches a return.
//
// It is an IN-PACKAGE test (package masters, not masters_test) because
// validateTDSChallan is unexported and must stay so: it is enforcement wiring
// behind RecordTDSChallan, not API. The precedent is internal/server's
// route_vocabulary_test.go, which makes the same call for routeAction.
//
// ⛔ TestChallanShapeRulesMatchTheMigration READS MIGRATION 0069 OFF DISK. That
// is the one anti-drift pair that has no other guard: a Go regexp looser than the
// CHECK turns this whole file into theatre, because the operator then gets the
// raw constraint violation the Go layer exists to prevent, and a Go regexp
// TIGHTER than the CHECK refuses a deposit the database would have accepted.

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// d is shorthand for a money literal. Every amount on this path is a decimal;
// a float64 1000.50 is not 1000.50, and the whole-rupee rule is a test on the
// fourth decimal place.
func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// validChallan is one believable deposit: ₹1,000 of tax deposited on 7 August
// 2026 at BSR branch 0004329 under challan serial 05122, minor head 200, for tax
// year 2026-27, paying one deduction of exactly ₹1,000.
//
// The CIN parts are the census fixture's (internal/platform/testdb) so that the
// two agree about what these identifiers look like — both carry leading zeros.
func validChallan() TDSChallanInput {
	return TDSChallanInput{
		BSRCode:      "0004329",
		DepositDate:  "2026-08-07",
		ChallanNo:    "05122",
		TaxRupees:    d("1000"),
		MinorHead:    "200",
		TaxYearStart: 2026,
		Allocations: []TDSChallanAllocationInput{{
			VoucherID:    uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			Section:      "194C",
			LegSeq:       0,
			AmountRupees: d("1000"),
		}},
	}
}

// TestAValidChallanIsAccceptedAndNormalised is the control. Without it every
// refusal below could be passing because validateTDSChallan refuses everything.
func TestAValidChallanIsAccceptedAndNormalised(t *testing.T) {
	in := validChallan()
	// Trailing and leading whitespace on the identifiers, which is what a paste
	// out of a bank portal actually contains. It must be stripped BEFORE the CIN
	// uniqueness check runs, or " 05122" and "05122" are two challans and the
	// duplicate-deposit refusal never fires.
	in.BSRCode = " 0004329 "
	in.ChallanNo = "05122\t"
	in.MinorHead = " 200"
	in.DepositDate = " 2026-08-07 "
	in.Allocations[0].Section = " 194C "

	out, err := validateTDSChallan(in)
	if err != nil {
		t.Fatalf("a valid challan was refused: %v", err)
	}
	if out.BSRCode != "0004329" {
		t.Errorf("BSR code normalised to %q, want %q", out.BSRCode, "0004329")
	}
	if out.ChallanNo != "05122" {
		t.Errorf("challan no normalised to %q, want %q", out.ChallanNo, "05122")
	}
	if out.MinorHead != "200" || out.DepositDate != "2026-08-07" {
		t.Errorf("minor head %q / deposit date %q not normalised", out.MinorHead, out.DepositDate)
	}
	if out.Allocations[0].Section != "194C" {
		t.Errorf("section normalised to %q, want %q", out.Allocations[0].Section, "194C")
	}
}

// TestLeadingZerosSurviveValidation is the identifier rule stated as a test.
//
// 0004329 is a BSR code and 4329 is a DIFFERENT BANK BRANCH; 05122 is a challan
// serial and 5122 is a different payment. One strconv.Atoi anywhere on this path
// destroys both, and the damage is discovered at the portal when the FVU matches
// the CIN against the .csi file. This test fails the moment anything on the path
// treats either identifier as a number.
func TestLeadingZerosSurviveValidation(t *testing.T) {
	in := validChallan()
	out, err := validateTDSChallan(in)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if out.BSRCode != "0004329" {
		t.Errorf("BSR code came back as %q — the leading zeros are part of the branch code, and "+
			"%q is a different branch of a different bank", out.BSRCode, out.BSRCode)
	}
	if out.ChallanNo != "05122" {
		t.Errorf("challan serial came back as %q — 05122 and 5122 are different payments",
			out.ChallanNo)
	}
	if !strings.HasPrefix(out.BSRCode, "000") {
		t.Errorf("BSR code lost its leading zeros: %q", out.BSRCode)
	}
}

// TestChallanRefusals walks every refusal validateTDSChallan can raise on the
// challan's own facts, and asserts BOTH that it refuses and that the sentence
// says the thing the operator needs to know. The `wants` strings are the load-
// bearing half: a refusal that says "invalid" is a refusal an operator cannot
// act on, and this is the only place that can hold that line.
func TestChallanRefusals(t *testing.T) {
	cases := []struct {
		name  string
		mut   func(*TDSChallanInput)
		wants []string
	}{
		// ---- The CIN -------------------------------------------------------
		{"bsr too short", func(c *TDSChallanInput) { c.BSRCode = "004329" },
			[]string{"SEVEN DIGITS", "LEADING ZEROS"}},
		{"bsr too long", func(c *TDSChallanInput) { c.BSRCode = "00043291" },
			[]string{"SEVEN DIGITS"}},
		{"bsr not numeric", func(c *TDSChallanInput) { c.BSRCode = "000432A" },
			[]string{"SEVEN DIGITS"}},
		{"bsr empty", func(c *TDSChallanInput) { c.BSRCode = "" },
			[]string{"SEVEN DIGITS"}},
		{"challan no empty", func(c *TDSChallanInput) { c.ChallanNo = "" },
			[]string{"ONE TO FIVE DIGITS"}},
		{"challan no six digits", func(c *TDSChallanInput) { c.ChallanNo = "051220" },
			[]string{"ONE TO FIVE DIGITS"}},
		{"challan no not numeric", func(c *TDSChallanInput) { c.ChallanNo = "5122A" },
			[]string{"ONE TO FIVE DIGITS"}},
		{"deposit date not iso", func(c *TDSChallanInput) { c.DepositDate = "07-08-2026" },
			[]string{"yyyy-mm-dd", "PAID TO THE GOVERNMENT"}},
		{"deposit date empty", func(c *TDSChallanInput) { c.DepositDate = "" },
			[]string{"yyyy-mm-dd"}},

		// ---- The minor head, which has NO DEFAULT --------------------------
		{"minor head missing", func(c *TDSChallanInput) { c.MinorHead = "" },
			[]string{"NO DEFAULT", "voluntary rather than demanded"}},
		{"minor head unknown", func(c *TDSChallanInput) { c.MinorHead = "300" },
			[]string{"Annexure 7", "200", "400", "100"}},

		// ---- The tax year, which is stored and not derived -----------------
		{"tax year too early", func(c *TDSChallanInput) { c.TaxYearStart = 2024 },
			[]string{"2025", "never computed from the date"}},
		{"tax year too late", func(c *TDSChallanInput) { c.TaxYearStart = 2201 },
			[]string{"2200"}},

		// ---- The four amounts ---------------------------------------------
		{"negative tax", func(c *TDSChallanInput) { c.TaxRupees = d("-1000") },
			[]string{"tax", "MAGNITUDES"}},
		{"negative interest", func(c *TDSChallanInput) { c.InterestRupees = d("-1") },
			[]string{"interest", "MAGNITUDES"}},
		{"negative fee", func(c *TDSChallanInput) { c.FeeRupees = d("-1") },
			[]string{"fee", "MAGNITUDES"}},
		{"negative penalty", func(c *TDSChallanInput) { c.PenaltyRupees = d("-1") },
			[]string{"penalty", "MAGNITUDES"}},
		// The format's own example, verbatim: 1000.50 is refused and 1000.00 is
		// not.
		{"paise in tax", func(c *TDSChallanInput) {
			c.TaxRupees = d("1000.50")
			c.Allocations[0].AmountRupees = d("1000.50")
		}, []string{"tax", "WHOLE RUPEES", "1000.50"}},
		{"paise in interest", func(c *TDSChallanInput) { c.InterestRupees = d("0.50") },
			[]string{"interest", "WHOLE RUPEES"}},
		{"paise in fee", func(c *TDSChallanInput) { c.FeeRupees = d("10.01") },
			[]string{"fee", "WHOLE RUPEES"}},
		{"paise in penalty", func(c *TDSChallanInput) { c.PenaltyRupees = d("10.9999") },
			[]string{"penalty", "WHOLE RUPEES"}},

		// ---- The nil challan, which is never stored ------------------------
		{"nothing deposited", func(c *TDSChallanInput) {
			c.TaxRupees = decimal.Zero
			c.Allocations = nil
		}, []string{"NIL challan", "synthesises"}},

		// ---- The allocations ----------------------------------------------
		{"tax with no allocation", func(c *TDSChallanInput) { c.Allocations = nil },
			[]string{"names no deduction", "field 5", "allocate-later"}},
		{"no tax but allocations", func(c *TDSChallanInput) {
			c.TaxRupees = decimal.Zero
			c.InterestRupees = d("250")
		}, []string{"NO TAX", "nothing here to allocate"}},
		{"allocation with no voucher", func(c *TDSChallanInput) {
			c.Allocations[0].VoucherID = uuid.Nil
		}, []string{"must name the voucher"}},
		{"allocation with no section", func(c *TDSChallanInput) {
			c.Allocations[0].Section = "  "
		}, []string{"names no section"}},
		{"allocation with negative leg", func(c *TDSChallanInput) {
			c.Allocations[0].LegSeq = -1
		}, []string{"0-based"}},
		{"allocation with negative amount", func(c *TDSChallanInput) {
			c.Allocations[0].AmountRupees = d("-1000")
		}, []string{"magnitude"}},
		{"the same deduction twice", func(c *TDSChallanInput) {
			c.Allocations = append(c.Allocations, c.Allocations[0])
			c.TaxRupees = d("2000")
		}, []string{"appears twice", "field 5"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := validChallan()
			tc.mut(&in)
			_, err := validateTDSChallan(in)
			if err == nil {
				t.Fatalf("%s was ACCEPTED; it must be refused", tc.name)
			}
			if !errors.Is(err, ErrInvalidTDSChallan) {
				t.Errorf("error does not wrap ErrInvalidTDSChallan, so the HTTP layer would "+
					"answer 500 instead of 400: %v", err)
			}
			for _, want := range tc.wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not mention %q, so it does not tell the operator "+
						"what to do about it.\ngot: %v", want, err)
				}
			}
		})
	}
}

// TestAnInterestOnlyChallanIsAccepted is the case the "tax with no allocation"
// refusal must NOT catch.
//
// Interest under s.396 on a late deposit, the late-filing fee and any penalty
// ride on a challan and are NOT tax (migration 0069 lines 90-96) — only the tax
// component discharges a deduction liability. So a challan that carries no tax
// discharges no deduction and correctly allocates to none. Refusing it would
// make a real payment unrecordable and leave the interest invisible.
func TestAnInterestOnlyChallanIsAccepted(t *testing.T) {
	in := validChallan()
	in.TaxRupees = decimal.Zero
	in.InterestRupees = d("250")
	in.Allocations = nil

	if _, err := validateTDSChallan(in); err != nil {
		t.Fatalf("an interest-only challan must be recordable — the s.396 interest on a late "+
			"deposit is a real payment and it is not tax: %v", err)
	}
}

// TestTheBoundariesOfEveryRangedRule pins the edges rather than the middles. An
// off-by-one in a `<` here is invisible to a test that only checks 2026 and 300.
func TestTheBoundariesOfEveryRangedRule(t *testing.T) {
	// Tax year: 2025 and 2200 are IN (migration 0069's
	// tds_challan_tax_year_sane is BETWEEN 2025 AND 2200, inclusive).
	for _, y := range []int{2025, 2200} {
		in := validChallan()
		in.TaxYearStart = y
		if _, err := validateTDSChallan(in); err != nil {
			t.Errorf("tax year %d is inside the migration's BETWEEN 2025 AND 2200 and must be "+
				"accepted: %v", y, err)
		}
	}
	// Challan serial: 1 digit and 5 digits are both legal (the format sizes the
	// field at 5).
	for _, no := range []string{"1", "0", "05122", "99999"} {
		in := validChallan()
		in.ChallanNo = no
		if _, err := validateTDSChallan(in); err != nil {
			t.Errorf("challan serial %q is one to five digits and must be accepted: %v", no, err)
		}
	}
	// A whole-rupee amount expressed with decimal places is still whole rupees:
	// 1000.0000 is the value the column actually holds.
	in := validChallan()
	in.TaxRupees = d("1000.0000")
	in.Allocations[0].AmountRupees = d("1000.0000")
	if _, err := validateTDSChallan(in); err != nil {
		t.Errorf("1000.0000 IS a whole rupee amount — the format's own example says value "+
			"'1000.00' is valid: %v", err)
	}
}

// TestIsWholeRupeesIsTheMigrationsTruncTest checks the predicate directly, at the
// fourth decimal place, which is where NUMERIC(19,4) hides a fraction.
func TestIsWholeRupeesIsTheMigrationsTruncTest(t *testing.T) {
	whole := []string{"0", "1", "1000", "1000.0000", "99999999999999"}
	fractional := []string{"1000.50", "0.0001", "1000.9999", "1.5"}
	for _, s := range whole {
		if !isWholeRupees(d(s)) {
			t.Errorf("%s is whole rupees and trunc(%s) = %s", s, s, s)
		}
	}
	for _, s := range fractional {
		if isWholeRupees(d(s)) {
			t.Errorf("%s carries paise and the format refuses it: \"value '1000.50' will not be "+
				"allowed\"", s)
		}
	}
}

// TestMinorHeadVocabularyIsClosedServedAndUndefaulted covers the three separate
// rules that hang off one three-value list.
func TestMinorHeadVocabularyIsClosedServedAndUndefaulted(t *testing.T) {
	heads := TDSChallanMinorHeads()
	if len(heads) != 3 {
		t.Fatalf("expected the three Annexure 7 minor heads, got %d", len(heads))
	}
	// The ORDER is the migration's CHECK order and is stable, because it is what
	// a picker renders. A map would reorder it per request.
	want := []string{"200", "400", "100"}
	for i, w := range want {
		if heads[i].Code != w {
			t.Errorf("minor head %d is %q, want %q — the order is the migration's and it is what "+
				"the picker renders", i, heads[i].Code, w)
		}
		if strings.TrimSpace(heads[i].Description) == "" {
			t.Errorf("minor head %q is served with no description; a picker of three bare numbers "+
				"invites whichever is first, and there is no default here", w)
		}
	}
	// SERVED AS A COPY. A caller that mutates the returned slice must not be able
	// to change what every later request offers.
	heads[0].Code = "999"
	if TDSChallanMinorHeads()[0].Code != "200" {
		t.Error("TDSChallanMinorHeads hands out its own backing array, so one caller can rewrite " +
			"the vocabulary for every other")
	}
	// AND THERE IS NO DEFAULT. Not "the default is 200" — no default at all:
	// choosing one would assert on the company's behalf that a deposit was
	// voluntary rather than demanded.
	in := validChallan()
	in.MinorHead = ""
	out, err := validateTDSChallan(in)
	if err == nil {
		t.Fatal("a challan with no minor head was accepted; migration 0069 lines 102-106 forbid " +
			"defaulting it")
	}
	if out.MinorHead != "" {
		t.Errorf("validation filled the minor head in as %q — nothing on this path may choose it",
			out.MinorHead)
	}
}

// TestChallanShapeRulesMatchTheMigration is the anti-drift pair with no other
// guard: the Go regexps against migration 0069's CHECK constraints, read off
// disk.
//
// It reads the FILE rather than the live catalogue (which deductor_status_test.go
// does, and which is the better test where a database is available) precisely so
// that it runs with Postgres down. A Go pattern looser than the CHECK means the
// operator gets `violates check constraint "tds_challan_bsr_shape"` — the exact
// outcome the Go layer exists to prevent — and a pattern tighter than the CHECK
// refuses a deposit the database would have taken.
func TestChallanShapeRulesMatchTheMigration(t *testing.T) {
	raw, err := os.ReadFile("../../migrations/0069_tds_challan.sql")
	if err != nil {
		t.Fatalf("read migration 0069: %v", err)
	}
	sql := string(raw)

	// The two regexps, byte for byte as the CHECK spells them.
	for _, pair := range []struct {
		goPattern string
		inCheck   string
	}{
		{bsrCodePattern.String(), `bsr_code ~ '^[0-9]{7}$'`},
		{challanNoPattern.String(), `challan_no ~ '^[0-9]{1,5}$'`},
	} {
		if !strings.Contains(sql, pair.inCheck) {
			t.Errorf("migration 0069 no longer contains %s — the Go pattern %q is now shadowing "+
				"something else, or nothing", pair.inCheck, pair.goPattern)
			continue
		}
		if !strings.Contains(pair.inCheck, pair.goPattern) {
			t.Errorf("the Go pattern %q is not the one in the CHECK (%s): a looser Go pattern "+
				"surfaces a raw constraint violation and a tighter one refuses a deposit the "+
				"database would accept", pair.goPattern, pair.inCheck)
		}
	}

	// The minor heads, and the fact that they are not defaulted.
	if !strings.Contains(sql, `minor_head IN ('200', '400', '100')`) {
		t.Error("migration 0069's minor_head CHECK has changed; TDSChallanMinorHeads is now a " +
			"copy that has drifted from it")
	}
	for _, m := range minorHeadDescriptions {
		if !strings.Contains(sql, "'"+m.Code+"'") {
			t.Errorf("minor head %q is served by Go and is not in migration 0069's CHECK — the "+
				"picker would offer a value the database refuses on save", m.Code)
		}
	}
	// The whole-rupee rule and the nil-challan rule, both of which this file's
	// sentences quote. If either constraint is relaxed, the Go refusal becomes a
	// rule the database no longer holds and the two sides must be reconciled
	// deliberately.
	for _, want := range []string{
		"tds_challan_amounts_whole_rupees",
		"tds_challan_something_was_deposited",
		"tds_challan_amounts_non_negative",
		"tds_challan_cin_unique",
		"tds_challan_allocation_one_per_deduction",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("constraint %s is gone from migration 0069, and tds_challan.go still writes "+
				"its refusal as though it were the authority", want)
		}
	}
}

// TestTheAllocationKeyIsTheNaturalKey is a compile-time-ish assertion written as
// a test, because the mistake it guards against is a FIELD, not a value.
//
// TDSChallanAllocationInput must key on (VoucherID, Section, LegSeq) and must NOT
// grow a DeductionID. internal/ledger/tds.go does
// `DELETE FROM tds_deduction WHERE company_id = $1 AND voucher_id = $2` and
// re-derives every row with a NEW id on each voucher ALTER, so a surrogate id
// stored here goes silently stale the first time anybody corrects a voucher —
// losing the record of a payment really made to the Government. Migration 0069
// has no foreign key to tds_deduction for exactly this reason.
func TestTheAllocationKeyIsTheNaturalKey(t *testing.T) {
	in := validChallan()
	a := in.Allocations[0]
	if a.VoucherID == uuid.Nil || a.Section == "" {
		t.Fatal("the fixture must exercise the natural key")
	}
	// The triple is what deduplication is keyed on. Two allocations differing
	// ONLY in leg_seq are two different deductions and both are legal; two
	// identical triples are the same deduction twice and neither is.
	in.Allocations = append(in.Allocations, TDSChallanAllocationInput{
		VoucherID: a.VoucherID, Section: a.Section, LegSeq: 1, AmountRupees: d("1000"),
	})
	in.TaxRupees = d("2000")
	if _, err := validateTDSChallan(in); err != nil {
		t.Errorf("one voucher can carry TWO deductions of the same section against the same "+
			"deductee — a consolidated contractor payment does, which is what leg_seq is for: %v",
			err)
	}
}
