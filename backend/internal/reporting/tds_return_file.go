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
// SO WHY IS Available STILL FALSE? BECAUSE A PINNED VERSION IS NECESSARY AND
// NOT SUFFICIENT — AND THE REMAINING GAPS ARE SCHEMA, NOT RESEARCH.
// ---------------------------------------------------------------------------
// That is the important change in kind. The old blockers were DOCUMENTS nobody
// here could fetch. The new ones are TABLES nobody here has written yet, and
// both are now specified down to the field by form140.Statement:
//
//  1. NO CHALLAN REGISTER. There is no tds_challan table, so nothing records
//     what was DEPOSITED. This is not merely the third-ranked item of T6 — the
//     format makes it a PREREQUISITE: every deductee record carries a challan
//     reference (DD field 5), so no deductee record can exist outside a
//     challan, not even under a nil one.
//
//  2. NO DEDUCTOR RETURN PROFILE. `company` holds gstin, address, city,
//     pincode and country. The Batch Header needs a TAN, a PAN, an Annexure 4
//     category, an e-mail and a contact number, plus the name, designation,
//     address, PAN and contact of the person responsible for deduction — none
//     of which any table holds. Every one is mandatory, and the TAN is not
//     derivable from the GSTIN.
//
// Validated stays false FOREVER, separately from all of the above. The FVU
// needs a Java runtime that does not exist here, so nothing this software
// produces has been validated by the department's own tool — and a "generated"
// return that reads as "validated" is worse than no return at all.
//
// The status is a VALUE ON THE REGISTER RESPONSE rather than a comment in this
// file, because the person who needs to know is the operator looking for their
// return — the same reasoning that puts the missing-QR notice on the face of a
// printed e-invoice.

import "github.com/khata/cored/internal/compliance/form140"

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

// ReturnFileRefusal states, in full, why no Form 140 or Form 143 file is
// produced from these books and what would unblock it.
func ReturnFileRefusal() ReturnFileStatus {
	return ReturnFileStatus{
		Available:     false,
		FormatVersion: form140.FormatVersion,
		FormatSource:  form140.FormatSource,
		Summary: "NO RETURN FILE IS GENERATED YET, BUT THE LAYOUT IS NO LONGER THE " +
			"REASON. The Income Tax Department's prescribed file format has been read " +
			"and is implemented — the record layouts, the four-digit PAYMENT CODE " +
			"LIST and the reason codes all come from it. What is missing is in YOUR " +
			"BOOKS, not in the specification: nothing here records the challans you " +
			"have deposited, and this company has no TAN or responsible-person " +
			"details on file.",
		Validated: false,
		ValidationNote: "Nothing this software produces has been validated by the " +
			"department's File Validation Utility. The FVU needs a Java runtime that " +
			"is not available here, so even once a file can be generated it must be " +
			"validated by you before filing. That stays true after every blocker " +
			"below is cleared.",
		Blockers: []ReturnFileBlocker{
			{
				What: "A CHALLAN REGISTER — what tax you have actually DEPOSITED",
				Where: "Your own bank challans: the BSR code of the receiving branch, the " +
					"challan serial number, the date of deposit, and the split of the " +
					"amount across tax, interest, fee and penalty. Nothing in this " +
					"software records any of it today.",
				Why: "Every deductee row of the return points at the challan that paid its " +
					"tax, so no deductee row can exist without one — that is structural, " +
					"not a matter of ordering. It is also the deposit and not the " +
					"deduction that stops interest running under s.396.",
			},
			{
				What: "This company's TAN, PAN and responsible-person details",
				Where: "Your TAN allotment letter, and whoever signs your returns. The TAN " +
					"is NOT the GSTIN and cannot be derived from it.",
				Why: "The return's header carries the deductor's TAN, PAN, deductor " +
					"category, address, e-mail and telephone, plus the name, designation, " +
					"address and PAN of the person responsible for deduction. This company " +
					"record holds none of them, and every one of them is mandatory.",
			},
			{
				What:  "The department's File Validation Utility, and the .csi challan file",
				Where: "Protean/TIN — the FVU needs a Java runtime; the .csi needs a TIN portal login.",
				Why: "A return is validated AGAINST the challan file: the FVU checks every " +
					"deposit claimed in the return against what the bank actually reported, " +
					"and the TAN in the statement must match the one in the .csi. Neither " +
					"can be reached from here, so a generated file must be validated by " +
					"you before it is filed.",
			},
		},
		WhatYouCanDoInstead: "Use this register to prepare the return in your filing " +
			"software or with your accountant: it carries every deduction and " +
			"collection for the period, per deductee, with the section, the four-digit " +
			"return code the deduction will be filed under, the rate and the amount. " +
			"What it cannot tell you is what has been DEPOSITED.",
	}
}
