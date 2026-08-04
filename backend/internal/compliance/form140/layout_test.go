package form140

// Tests over the LAYOUT TABLES themselves — the transcription, not the
// behaviour. They are in the internal test package because the tables are
// unexported and because what they assert is that the transcription matches the
// workbook, which is a fact about this file and not about the API.

import (
	"fmt"
	"testing"
)

// TestRecordFieldCounts pins the number of "Sr. No." rows in each record's
// table in the workbook. These four numbers ARE the format: every one of them
// is also the delimiter count plus one, so a field silently added or dropped
// shifts every column after it.
func TestRecordFieldCounts(t *testing.T) {
	want := map[string]int{"FH": 18, "BH": 72, "CD": 30, "DD": 45}
	for _, rec := range allRecords {
		if got := len(rec.fields); got != want[rec.typ] {
			t.Errorf("%s (%s) has %d fields, want %d", rec.typ, rec.title, got, want[rec.typ])
		}
	}
}

// TestFileHeaderHasEighteenFields pins the ONE PLACE THE SPEC CONTRADICTS
// ITSELF, so that resolving it the other way is a deliberate edit rather than a
// drift.
//
// General Note 10 says in prose: "the total number of fields in 'File Header
// record' is 16, hence the total no. of delimiters in 'File Header record'
// would be 15." The File Header table in the same workbook lists eighteen rows.
// We follow the TABLE — see layout.go's header for the reasoning. If the FVU
// ever rejects the header, this test is the first thing to change and the
// change should be loud.
func TestFileHeaderHasEighteenFields(t *testing.T) {
	if len(fileHeader.fields) != 18 {
		t.Fatalf("file header has %d fields, want 18", len(fileHeader.fields))
	}
	// The two rows beyond the prose's count, named, so the reason for the
	// discrepancy stays visible: they are trailing "Not applicable" hash fields.
	for _, no := range []int{17, 18} {
		fl := mustField(t, &fileHeader, no)
		if fl.k != kindUnused {
			t.Errorf("FH field %d (%s) is not a \"not applicable\" field; the 16-vs-18 "+
				"discrepancy was explained by those two being trailing hash fields", no, fl.name)
		}
	}
}

// TestSerialNumbersAreContiguousFromOne is what makes "set by field number"
// safe: every layout's Sr. Nos. must run 1..N with no gaps and no repeats, or
// the slot index and the spec's numbering part company.
func TestSerialNumbersAreContiguousFromOne(t *testing.T) {
	for _, rec := range allRecords {
		seen := map[int]bool{}
		for i, fl := range rec.fields {
			if fl.no != i+1 {
				t.Errorf("%s: field at position %d carries Sr. No. %d (%s); the table must be "+
					"in the workbook's own order", rec.typ, i+1, fl.no, fl.name)
			}
			if seen[fl.no] {
				t.Errorf("%s: Sr. No. %d appears twice", rec.typ, fl.no)
			}
			seen[fl.no] = true
		}
	}
}

// TestUnusedFieldsHaveNoSize checks the transcription of the workbook's
// "Size 0 / NA" column: a field whose Remarks say "No value should be
// specified" carries no size, and one that does carry a size is a real field
// that was mistranscribed as unused.
func TestUnusedFieldsHaveNoSize(t *testing.T) {
	for _, rec := range allRecords {
		for _, fl := range rec.fields {
			if fl.k == kindUnused && fl.size != 0 {
				t.Errorf("%s field %d (%s) is marked unused but carries size %d",
					rec.typ, fl.no, fl.name, fl.size)
			}
			if fl.k != kindUnused && fl.size == 0 {
				t.Errorf("%s field %d (%s) is a real field with no size", rec.typ, fl.no, fl.name)
			}
		}
	}
}

// TestTheConditionalFieldsAreTheDocumentedSet pins exactly which fields the
// generic presence check in row.render SKIPS.
//
// This is the test that keeps the two halves of validation honest. render()
// checks unconditional mandatory fields; build.go checks the conditional ones.
// A field marked cond with no rule written for it is a MANDATORY FIELD THAT
// NOTHING CHECKS — it would be emitted empty and rejected at the portal. So
// adding one to a layout must fail here until its rule exists and this list is
// updated in the same edit.
func TestTheConditionalFieldsAreTheDocumentedSet(t *testing.T) {
	want := map[string][]int{
		// BH 9  token of previous statement — mandatory iff field 52 is Y.
		// BH 28 deductor e-mail — mandatory iff BH 42 is absent.
		// BH 42 responsible person's e-mail — the mirror of BH 28.
		// BH 54 state name — mandatory for S/E/H/N, forbidden otherwise.
		// BH 57 ministry — mandatory for A/D/G, optional for E/H/L/N, else forbidden.
		// BH 58 ministry (other) — required iff field 57 is 99.
		// BH 68 AIN — only for Government deductors paying by book entry.
		"BH": {9, 28, 42, 54, 57, 58, 68},
		// CD 13 mode of payment — absent for a nil challan.
		// CD 15 BSR code — mandatory except for a nil challan.
		// CD 17 bank challan number — absent for a nil challan.
		// CD 19 deposit date — pinned to the quarter end for a nil challan.
		// CD 23 minor head — Annexure 7, and code 100 pulls in two extra rules
		//       (the date must fall in the quarter, and must not be after the
		//       payment date of any deductee record mapped to the challan).
		"CD": {13, 15, 17, 19, 23},
		// DD 22/23 cash-withdrawal amounts — one section code each.
		// DD 27 date of deduction — mandatory iff tax deducted > 0, else forbidden.
		// DD 33 s.395 certificate number — mandatory iff flag A.
		"DD": {22, 23, 27, 33},
	}
	for _, rec := range allRecords {
		var got []int
		for _, fl := range rec.fields {
			if fl.cond {
				got = append(got, fl.no)
			}
		}
		if fmt.Sprint(got) != fmt.Sprint(want[rec.typ]) {
			t.Errorf("%s conditional fields are %v, want %v — if you added one, write its rule "+
				"in build.go and say what the condition is here", rec.typ, got, want[rec.typ])
		}
	}
}

func mustField(t *testing.T, rec *record, no int) field {
	t.Helper()
	for _, fl := range rec.fields {
		if fl.no == no {
			return fl
		}
	}
	t.Fatalf("%s has no field %d", rec.typ, no)
	return field{}
}
