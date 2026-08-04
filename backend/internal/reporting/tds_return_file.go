package reporting

// I-7 — THE RETURN FILE. THE FORMAT IS READ; THE BOOKS CANNOT YET FILL IT.
//
// docs/design/t6-model.md §4 invariant I-7: "No FVU file is emitted without a
// pinned fileFormatVersion traced to a READ PRIMARY SOURCE."
//
// ---------------------------------------------------------------------------
// ⛔ THIS FILE USED TO SAY THE LAYOUT HAD NOT BEEN READ. THAT WAS TRUE, AND
// THEN IT WAS FALSE FOR TWO DAYS BEFORE ANYBODY NOTICED.
// ---------------------------------------------------------------------------
// The blocker was recorded as "incometaxindia.gov.in returns 403 to every
// automated request". That is still true. But the FILE FORMAT was never on that
// site — it is Protean/TIN's, and docs/statutory/form-140-file-format.xlsx has
// been committed here since cfb83ff. The refusal was correct when written and
// was overtaken the next day, because it was phrased as a fact about a SITE
// rather than as a question about a DOCUMENT. HANDOFF-NEXT-THREE.md §0.
//
// internal/compliance/form140 now encodes that workbook: all four record
// layouts, every annexure, and the emitter. FormatVersion is pinned to "1.0"
// and traced to the document, which is what I-7 asks for.
//
// ---------------------------------------------------------------------------
// ⛔ AND THEN THE SAME THING HAPPENED AGAIN, ONE LAYER DOWN.
// ---------------------------------------------------------------------------
// This file used to hold ReturnFileRefusal(), a NILADIC function that returned
// the same three blockers for every company alive. Two of them said, verbatim,
// "There is no tds_challan table" and "This company record holds none of them".
// Migrations 0069 and 0070 falsified the first; the write path over them
// falsifies the second for any company that fills the form in.
//
// THE DEFECT WAS NEVER THE WORDING — IT WAS THE ARITY. A refusal that takes no
// arguments can only make claims about THE SOFTWARE, and a claim about the
// software goes stale the moment somebody ships something. A refusal that takes
// a company and a period can only make claims about THOSE BOOKS, and it is
// re-answered on every request, so it cannot go stale at all.
//
// ReturnFileRefusal() is therefore GONE, not merely corrected — leaving it would
// leave a caller able to reach the constant version. Service.ReturnFileStatusFor
// in tds_return_status.go replaces it, and this file keeps only the two types
// they share.
//
// Validated stays false FOREVER, separately from every blocker. The FVU needs a
// Java runtime that does not exist here, so nothing this software produces has
// been validated by the department's own tool — and a "generated" return that
// reads as "validated" is worse than no return at all.
//
// The status is a VALUE ON THE REGISTER RESPONSE rather than a comment in this
// file, because the person who needs to know is the operator looking for their
// return — the same reasoning that puts the missing-QR notice on the face of a
// printed e-invoice.

// ReturnFileBlocker names one thing that must exist before a return file can be
// emitted, and where it comes from.
type ReturnFileBlocker struct {
	What  string `json:"what"`
	Where string `json:"where"`
	Why   string `json:"why"`
}

// ReturnFileStatus is the machine-readable status of the return file.
type ReturnFileStatus struct {
	// Available reports whether a file can be generated from these books. A
	// client must branch on THIS and on nothing else — in particular not on the
	// presence of FormatVersion, which is now non-empty while Available is
	// still false.
	Available bool `json:"available"`
	// FormatVersion is the pinned, read-from-primary layout version.
	//
	// ⚠️ ITS MEANING HAS CHANGED. It used to be empty, and its emptiness was
	// I-7 in one field. It is now "1.0", read from the prescribed file format —
	// so an empty value still means "no primary source", but a non-empty one no
	// longer means "ready to file". FormatSource names the document, because a
	// version number with no document behind it is not a citation.
	FormatVersion string `json:"format_version"`
	FormatSource  string `json:"format_source"`
	Summary       string `json:"summary"`
	// Validated is ALWAYS false and is sent ALWAYS, including once a file can be
	// generated. Omitting it once emission works would quietly upgrade
	// "generated" to "validated" in a reader's mind.
	Validated           bool                `json:"validated"`
	ValidationNote      string              `json:"validation_note"`
	Blockers            []ReturnFileBlocker `json:"blockers"`
	WhatYouCanDoInstead string              `json:"what_you_can_do_instead"`
}
