// Package form140 emits the Income Tax Department's prescribed quarterly
// non-salary TDS return — Form Number 140, the successor to Form 26Q.
//
// ===========================================================================
// PROVENANCE — THIS IS READ FROM THE PRIMARY SOURCE, AND THAT IS THE POINT
// ===========================================================================
// SOURCE: docs/statutory/form-140-file-format.xlsx, sheet "File Format_R" —
// "Prescribed File Format by Income Tax Department / File Format for Non-Salary
// TDS File - Form Number 140 (26Q) - Q1 to Q4 (Version 1.0) for Tax Year
// 2026-27 and onwards". Committed to this repository at cfb83ff.
//
// FormatVersion below is that version string, and it is what invariant I-7 of
// docs/design/t6-model.md §4 asks for: "No FVU file is emitted without a pinned
// fileFormatVersion traced to a READ PRIMARY SOURCE." Everything in layout.go
// and annexure.go is transcribed from that workbook — the field order, the
// sizes, the M/O column, the constants, and all seven annexures. Nothing here
// comes from a commentary.
//
// ⚠️ THE REFUSAL THIS REPLACES WAS CORRECT WHEN WRITTEN. reporting's
// ReturnFileRefusal() said the layout "has not been read from a primary source",
// and while that was true, guessing at it would have filed real people's tax
// under invented codes. It stopped being true the day the workbook landed, and
// nobody re-checked for two days. See HANDOFF-NEXT-THREE.md §0.
//
// ===========================================================================
// WHAT THIS PACKAGE IS NOT
// ===========================================================================
//  1. IT IS NOT VALIDATED, AND MUST NEVER BE DESCRIBED AS VALIDATED. The
//     department's File Validation Utility is a Java program plus Protean's RPU
//     zip, neither of which exists in this environment. This package produces a
//     file built to a read specification; whether the FVU accepts it is
//     unknown, and the operator must run it. Every caller that surfaces the
//     output carries that sentence.
//  2. IT HAS NO I/O AND NO DATABASE. It is a pure leaf beside internal/
//     compliance, exactly as gstr is for GST. Build takes a fully-specified
//     Statement and returns bytes. Sourcing that Statement from the books is a
//     separate concern, and it is BLOCKED on two schema gaps this package makes
//     concrete rather than abstract — see Statement's doc comment.
//  3. IT DOES NOT DECIDE THE PAYMENT CODE. compliance.TDSReturnCode does that,
//     at the moment the deduction is derived, from the (section, deductee type)
//     pair. This package validates that the code it was handed is one Annexure 2
//     knows, and refuses otherwise.
//
// ===========================================================================
// THE PHYSICAL FORMAT, from the workbook's General Notes 1-15
// ===========================================================================
// ASCII, ".txt". Every record including the last ends with CRLF (hex 0D 0A).
// A "^"-delimited VARIABLE-width file: no leading zeros for numerics, no
// trailing spaces for character fields. Constants ("FH", "R", "D") in BLOCK
// CAPITALS. Amounts at 2 decimals; the rate of deduction at 4 ("2" is written
// "2.0000"). Dates ddmmyyyy, and NO DATE MAY BE IN THE FUTURE.
//
// General Note 10 is the one that is easy to break and hard to notice: "The
// total number of delimiters in any type of record should be one less than the
// total number of fields in the respective record." It is not enforced here by
// a check — it is STRUCTURAL. Every record is serialised by walking its layout
// and emitting exactly one value per field, so the count cannot drift from the
// spec without the layout table itself changing.
package form140

// FormatVersion is the prescribed file format version this package implements,
// verbatim from the workbook's title row.
//
// It is exported and non-empty precisely because I-7 keys off it: an emitter
// with an empty format version has not read a primary source and must not
// produce a file. Anything that reports a return's status to a human reads this
// rather than restating "1.0" of its own.
const FormatVersion = "1.0"

// FormatSource names the document FormatVersion was read from, for the same
// reason internal/statutory records the NIC schema it validated against: a
// version number with no document behind it is not a citation.
const FormatSource = "Income Tax Department prescribed file format, " +
	"Form Number 140 (26Q) Q1-Q4, Version 1.0, Tax Year 2026-27 and onwards " +
	"(docs/statutory/form-140-file-format.xlsx, sheet \"File Format_R\")"

// FormNumber is the value BH field 5 must carry.
const FormNumber = "140"
