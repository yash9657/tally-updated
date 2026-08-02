package reporting

// I-7 — THE RETURN FILE, AND WHY THERE ISN'T ONE.
//
// docs/design/t6-model.md §4 invariant I-7: "No FVU file is emitted without a
// pinned fileFormatVersion traced to a READ PRIMARY SOURCE." §1.6 defers the
// file for the same reason and §6.5 records why it could not be read:
// incometaxindia.gov.in returns 403 to every automated request from this
// environment, including the direct document URLs for Form 140 and its FAQ.
//
// ---------------------------------------------------------------------------
// WHY A REFUSAL AND NOT A BEST EFFORT
// ---------------------------------------------------------------------------
// Enough is known from secondary sources to PRODUCE something file-shaped:
// caret-delimited, FH/BH/CD/DD record types, CRLF, amounts at 2 dp, rate at
// 4 dp, dates ddmmyyyy. Every one of those came from a commentary rather than
// from the File Format document, and one of the same sources carried
// pre-Finance-Act-2025 thresholds that the Act text disproves — so it is not
// trustworthy on layout either.
//
// A file built from that would be REJECTED AT THE PORTAL AT BEST. At worst it
// would be accepted with the wrong payment code against each deductee, which
// files a contractor payment as professional fees under somebody's PAN — a
// wrong return in the deductee's name, discovered by them, months later.
// HANDOFF-FEATURE-LOCK.md §1.1 is unambiguous: never invent a code; cite it or
// refuse. This is the refusal, made visible rather than left as an absence.
//
// It is a VALUE ON THE REGISTER RESPONSE rather than a comment in this file,
// because the person who needs to know is the operator looking for their
// return — not the next reader of the source. That is the same reasoning that
// puts the missing-QR notice on the face of a printed e-invoice.

// ReturnFileBlocker names one thing that must be obtained before a return file
// can be emitted, and where it comes from.
type ReturnFileBlocker struct {
	What  string `json:"what"`
	Where string `json:"where"`
	Why   string `json:"why"`
}

// ReturnFileStatus is the machine-readable refusal.
type ReturnFileStatus struct {
	// Available is false and will stay false until a primary source is read and
	// FormatVersion is pinned. A client must branch on THIS rather than on the
	// presence of the other fields.
	Available bool `json:"available"`
	// FormatVersion is the pinned, read-from-primary layout version. Empty is
	// exactly what makes Available false — I-7 in one field.
	FormatVersion string `json:"format_version"`
	Summary       string `json:"summary"`
	// Validated is ALWAYS false and is sent ALWAYS, including once a file can be
	// generated. The FVU needs a JRE that does not exist in this environment, so
	// nothing this software produces has been validated by the department's own
	// tool. Omitting the field once emission works would quietly upgrade
	// "generated" to "validated" in a reader's mind.
	Validated           bool                `json:"validated"`
	ValidationNote      string              `json:"validation_note"`
	Blockers            []ReturnFileBlocker `json:"blockers"`
	WhatYouCanDoInstead string              `json:"what_you_can_do_instead"`
}

// ReturnFileRefusal states, in full, why no Form 140 or Form 143 file is
// produced and what would unblock it.
func ReturnFileRefusal() ReturnFileStatus {
	return ReturnFileStatus{
		Available:     false,
		FormatVersion: "",
		Summary: "NO RETURN FILE IS GENERATED. The deductions and collections below " +
			"are complete and correct, but the layout a return must be written in " +
			"has not been read from a primary source, and a file guessed from " +
			"commentary would file real people's tax under the wrong codes.",
		Validated: false,
		ValidationNote: "Nothing this software produces has been validated by the " +
			"department's File Validation Utility. The FVU needs a Java runtime that " +
			"is not available here, so even once a file can be generated it must be " +
			"validated by you before filing.",
		Blockers: []ReturnFileBlocker{
			{
				What: "The Form 140 layout (quarterly non-salary TDS return)",
				Where: "incometaxindia.gov.in — /documents/d/guest/fn-140 and its FAQ. " +
					"Both return 403 to automated requests from this environment; a " +
					"person with a browser can download them in a minute.",
				Why: "It fixes the record order and the field widths. Without it there " +
					"is no layout to write, only a guess at one.",
			},
			{
				What:  "The Protean/TIN e-TDS File Format for the quarterly statement",
				Where: "Protean (formerly NSDL) TIN website, e-TDS/e-TCS file formats.",
				Why: "It fixes the column order and the PAYMENT CODE LIST. The codes " +
					"are the dangerous part: 194J professional fees and 194J directors' " +
					"fees share a rate and differ only by code, so a wrong list files " +
					"one as the other under the deductee's PAN.",
			},
			{
				What:  "The Form 143 (quarterly TCS return) collection codes",
				Where: "Same two sources.",
				Why: "The collection side has no code mapping at all in this build — " +
					"migration 0067 deliberately stores no return_code rather than a " +
					"fabricated one.",
			},
			{
				What: "A challan register, and the TIN .csi challan file",
				Where: "The .csi is downloaded per TAN from the TIN portal, which is " +
					"also unreachable here.",
				Why: "A return is validated AGAINST the challan file: the FVU checks " +
					"every deposit claimed in the return against what the bank actually " +
					"reported. There is no challan table in this build yet, so there is " +
					"nothing to reconcile even if the layout were known.",
			},
		},
		WhatYouCanDoInstead: "Use this register to prepare the return in your filing " +
			"software or with your accountant: it carries every deduction and " +
			"collection for the period, per deductee, with the section, rate and " +
			"amount. What it cannot tell you is what has been DEPOSITED.",
	}
}
