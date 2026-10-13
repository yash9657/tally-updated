package server

// PER-COMPANY PRINT TEMPLATES — PROVED AGAINST THE PRINTED BYTES.
//
// ⛔ THE ORACLE IS THE PDF, NOT THE ROW, AND THAT IS THE WHOLE POINT OF THIS FILE.
// A test that PUTs an override and reads it back proves the table works — which it
// already did, for months, while no route could reach it and no document was
// affected by anything in it. It CANNOT see a printer that ignores the override,
// which is the failure that matters to a company whose invoice still says the
// wrong thing after somebody changed it. So every assertion here goes through
// GET /print/... and looks at the text the renderer actually drew.
//
// That oracle is available because the READ side was already wired:
// printingdb/document.go honours overrides on both document paths and
// handler_print_reports.go honours them on the report letterhead. Write one,
// print, and the bytes change — a genuine end-to-end check rather than a mock.

import (
	"net/http"
	"strings"
	"testing"
)

// receiptDefaults are what a receipt prints for a company that has customised
// nothing. They come from printing.DefaultTitle/DefaultDeclaration via the
// renderer, and TestPrintTemplateSurfaceGolden is what keeps those honest.
const (
	receiptDefaultTitle       = "RECEIPT VOUCHER"
	receiptDefaultDeclaration = "Received subject to realisation of the instrument."
)

// TestAPrintTemplateOverrideChangesThePrintedDocumentAndClearingItPutsItBack is
// the end-to-end proof for track C, in both directions.
//
// The two directions matter equally. A one-way door is not a setting: a company
// that sets a declaration once and can never return to the built-in default has
// been given a trap, not a feature. So this drives set → print → clear → print
// and asserts the paper at every step.
func TestAPrintTemplateOverrideChangesThePrintedDocumentAndClearingItPutsItBack(t *testing.T) {
	s := payrollServer(t)
	f := newPrintFixture(t, s)
	docPath := f.base + "/print/vouchers/receipt/" + f.receiptID
	tmplPath := f.base + "/print-templates/receipt"

	// ---------------------------------------------------------------
	// BEFORE: the built-in layout, with no row in print_template.
	// ---------------------------------------------------------------
	rec, body := aclRequest(t, s, http.MethodGet, tmplPath, f.ownerTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET the receipt template on a fresh company = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if body["customised"] != false {
		t.Errorf("a company with no print_template row reports customised=%v, want false. "+
			"The screen uses this to decide whether there is anything to CLEAR, and to tell "+
			"'never asked' apart from 'deliberately blank'", body["customised"])
	}
	if defs, _ := body["defaults"].(map[string]any); defs == nil ||
		defs["title"] != receiptDefaultTitle || defs["declaration"] != receiptDefaultDeclaration {
		t.Errorf("the response's built-in defaults are %v, want title=%q declaration=%q. "+
			"An editing form shows these as the placeholder in an empty box; without them the "+
			"box reads as 'this company has no declaration'",
			body["defaults"], receiptDefaultTitle, receiptDefaultDeclaration)
	}

	before := pdfText(t, printGet(t, s, docPath, f.ownerTok), "the receipt before any override")
	for _, want := range []string{receiptDefaultTitle, receiptDefaultDeclaration} {
		if !strings.Contains(before, want) {
			t.Fatalf("the receipt printed with no override does not contain the built-in %q.\n"+
				"THE BASELINE IS WRONG, so every assertion below would be vacuous: there would be "+
				"nothing for an override to replace.\n--- printed ---\n%s", want, before)
		}
	}

	// ---------------------------------------------------------------
	// SET: one PUT, and the printed document must change.
	// ---------------------------------------------------------------
	const (
		customTitle       = "MONEY RECEIPT"
		customDeclaration = "Subject to realisation of the cheque at our bankers only."
		customFooter      = "This receipt is valid without a revenue stamp."
	)
	rec, body = aclRequest(t, s, http.MethodPut, tmplPath, f.ownerTok, map[string]any{
		"title":        customTitle,
		"declaration":  customDeclaration,
		"footer_lines": []string{customFooter},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT the receipt template = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if body["customised"] != true {
		t.Errorf("after a PUT the template reports customised=%v, want true (%s)",
			body["customised"], rec.Body.String())
	}

	after := pdfText(t, printGet(t, s, docPath, f.ownerTok), "the receipt after the override")

	// ⛔ THE ASSERTIONS THIS TRACK EXISTS FOR. Each names one field, so a handler
	// that drops a single column is reported by the field it dropped rather than
	// as one opaque "the PDF differs".
	for _, tc := range []struct {
		field, want string
	}{
		{"title", customTitle},
		{"declaration", customDeclaration},
		{"footer_lines", customFooter},
	} {
		if !strings.Contains(after, tc.want) {
			t.Errorf("the printed receipt does NOT contain the overridden %s (%q).\n"+
				"The row was stored and the paper did not change, which is the exact failure a "+
				"read-back test cannot see: either the handler dropped the column or the "+
				"renderer is not reading it.\n--- printed ---\n%s", tc.field, tc.want, after)
		}
	}
	// And the built-ins it REPLACES must be gone. An override that merely appends
	// leaves a receipt carrying two titles and two contradictory declarations.
	for _, gone := range []string{receiptDefaultTitle, receiptDefaultDeclaration} {
		if strings.Contains(after, gone) {
			t.Errorf("the printed receipt STILL contains the built-in %q alongside the "+
				"override. The override must REPLACE the default, not print beside it — a "+
				"document with two declarations says nothing reliable.\n--- printed ---\n%s",
				gone, after)
		}
	}

	// ---------------------------------------------------------------
	// CLEAR: the DELETE must put the built-in layout back.
	// ---------------------------------------------------------------
	rec, body = aclRequest(t, s, http.MethodDelete, tmplPath, f.ownerTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE the receipt template = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if body["cleared"] != true {
		t.Errorf("DELETE reports cleared=%v, want true: there WAS an override to remove (%s)",
			body["cleared"], rec.Body.String())
	}
	if body["customised"] != false {
		t.Errorf("after a DELETE the template reports customised=%v, want false — the screen "+
			"would go on offering to clear an override that is gone", body["customised"])
	}

	restored := pdfText(t, printGet(t, s, docPath, f.ownerTok), "the receipt after clearing")
	for _, want := range []string{receiptDefaultTitle, receiptDefaultDeclaration} {
		if !strings.Contains(restored, want) {
			t.Errorf("after clearing the override the receipt does not print the built-in %q "+
				"again. A company that customised its layout once is STUCK with it, which makes "+
				"this a trap rather than a setting.\n--- printed ---\n%s", want, restored)
		}
	}
	for _, gone := range []string{customTitle, customDeclaration, customFooter} {
		if strings.Contains(restored, gone) {
			t.Errorf("after clearing the override the receipt still prints %q. The row was "+
				"removed and the document did not follow.\n--- printed ---\n%s", gone, restored)
		}
	}

	// A second DELETE is a success that says there was nothing to do. Clearing an
	// override a colleague already cleared is not an error, and a 404 here would
	// make the screen report a failure for the state the operator wanted.
	rec, body = aclRequest(t, s, http.MethodDelete, tmplPath, f.ownerTok, nil)
	if rec.Code != http.StatusOK || body["cleared"] != false {
		t.Errorf("a second DELETE = %d cleared=%v, want 200 and cleared=false (idempotent, and "+
			"honest about having found nothing) (%s)", rec.Code, body["cleared"], rec.Body.String())
	}
}

// TestAReportTemplateRefusesTheFieldsAReportCannotPrintAndHonoursTheThreeItCan
// is the ragged-grid half, and it is the one that keeps the screen honest.
//
// ⛔ RenderReport READS THREE OF THE SEVEN OVERRIDABLE FIELDS. Storing a
// declaration for the `report` kind would be accepted, stored, and never printed:
// the operator types a certification, the screen says saved, and no statement ever
// carries it. Nothing errors and nothing logs. So the route refuses it — and this
// test proves BOTH halves, because a route that refused everything would also
// pass a test that only checked the refusal.
func TestAReportTemplateRefusesTheFieldsAReportCannotPrintAndHonoursTheThreeItCan(t *testing.T) {
	s := payrollServer(t)
	f := newPrintFixture(t, s)
	tmplPath := f.base + "/print-templates/report"
	reportPath := f.base + "/print/reports/trial-balance?as_on=2026-07-31"

	// The honoured half: a letterhead line reaches the paper.
	const letterhead = "Registered Office: 14 Residency Road, Bengaluru 560025"
	rec, _ := aclRequest(t, s, http.MethodPut, tmplPath, f.ownerTok, map[string]any{
		"header_lines": []string{letterhead},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT header_lines for the report kind = %d, want 200: RenderReport honours "+
			"header_lines and refusing it would take away the one thing a company most wants "+
			"on its statements (%s)", rec.Code, rec.Body.String())
	}
	printed := pdfText(t, printGet(t, s, reportPath, f.ownerTok), "the trial balance with a letterhead")
	if !strings.Contains(printed, letterhead) {
		t.Errorf("the printed Trial Balance does not carry the overridden letterhead %q.\n"+
			"--- printed ---\n%s", letterhead, printed)
	}

	// The refused half: each field RenderReport ignores, sent with real content.
	for _, tc := range []struct {
		name string
		body map[string]any
	}{
		{"declaration", map[string]any{"declaration": "We certify these accounts are true and correct."}},
		{"terms", map[string]any{"terms": "Payable within 30 days."}},
		{"signature_label", map[string]any{"signature_label": "Chartered Accountant"}},
		{"bank_lines", map[string]any{"show_bank_details": true, "bank_lines": []string{"A/c 001122334455"}}},
	} {
		rec, _ := aclRequest(t, s, http.MethodPut, tmplPath, f.ownerTok, tc.body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("PUT %s for the report kind = %d, want 400. RenderReport does not read it, "+
				"so accepting it stores a value that will never print — the operator typed a "+
				"%s, was told it saved, and no statement will ever show it (%s)",
				tc.name, rec.Code, tc.name, rec.Body.String())
			continue
		}
		if msg := rec.Body.String(); !strings.Contains(msg, "report") {
			t.Errorf("the refusal of %s does not name the document kind, so an operator cannot "+
				"tell why it was refused: %s", tc.name, msg)
		}
	}

	// ⚠️ PRESENCE IS NOT CONTENT. A screen that submits its whole form shape with
	// the unused boxes EMPTY is behaving correctly and must not get a 400 — that
	// would make the honest client the broken one.
	rec, _ = aclRequest(t, s, http.MethodPut, tmplPath, f.ownerTok, map[string]any{
		"header_lines":      []string{letterhead},
		"declaration":       "",
		"terms":             "",
		"signature_label":   "",
		"bank_lines":        []string{},
		"show_bank_details": false,
	})
	if rec.Code != http.StatusOK {
		t.Errorf("PUT the report template with the unhonoured fields present but EMPTY = %d, "+
			"want 200. Only typed CONTENT is refused; refusing the mere shape of a full form "+
			"punishes a correct client (%s)", rec.Code, rec.Body.String())
	}
}

// TestThePrintTemplateRouteRefusesADocumentKindItCannotPrint checks the path
// segment is validated before anything is opened, with a message that says what
// the vocabulary IS — the difference between an answer and a support ticket.
func TestThePrintTemplateRouteRefusesADocumentKindItCannotPrint(t *testing.T) {
	s := payrollServer(t)
	f := newPrintFixture(t, s)

	// "invoice" is the plausible wrong answer: it is what everybody calls the
	// document, and the stored vocabulary spells it "tax-invoice".
	for _, kind := range []string{"invoice", "quote", "TAX-INVOICE"} {
		rec, _ := aclRequest(t, s, http.MethodGet, f.base+"/print-templates/"+kind, f.ownerTok, nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("GET the template for unknown kind %q = %d, want 400 (%s)",
				kind, rec.Code, rec.Body.String())
			continue
		}
		if msg := rec.Body.String(); !strings.Contains(msg, "tax-invoice") {
			t.Errorf("refusing kind %q does not name the vocabulary, so the caller cannot "+
				"correct itself: %s", kind, msg)
		}
	}
}

// TestThePrintTemplateWriteIsGovernedAsCompanyConfigurationAndTheReadIsNot pins
// the authority choice, because it is the one decision here that cannot be
// corrected later without an ACL migration.
//
// ⛔ THE TOKEN IS `company`. Migration 0011 seeds the Data Entry level a coarse
// `allow display report`, so ANY resource spelled report:<something> is granted to
// every operator in every tenant the day it appears — and this write changes what
// the company's invoices say to its customers. Migration 0022 seeds Data Entry
// `allow display company`, a DISPLAY, so `alter company` is default-denied for
// every seeded non-Owner level with no migration at all. This test is what makes
// that claim falsifiable rather than a comment.
func TestThePrintTemplateWriteIsGovernedAsCompanyConfigurationAndTheReadIsNot(t *testing.T) {
	s := payrollServer(t)
	f := newPrintFixture(t, s)
	tmplPath := f.base + "/print-templates/tax-invoice"

	// A seeded Data Entry level MAY read it: migration 0022 grants `display
	// company` so the client can hydrate the home state code, and this route's
	// read shares that token deliberately.
	if rec, _ := aclRequest(t, s, http.MethodGet, tmplPath, f.dataEntry, nil); rec.Code != http.StatusOK {
		t.Errorf("a Data Entry operator reading the invoice template = %d, want 200: the read "+
			"is a Display over `company`, which migration 0022 seeds (%s)", rec.Code, rec.Body.String())
	}

	// And MAY NOT write it. This is the security argument, made concrete.
	rec, _ := aclRequest(t, s, http.MethodPut, tmplPath, f.dataEntry, map[string]any{
		"declaration": "Anything at all.",
	})
	if rec.Code != http.StatusForbidden {
		t.Errorf("a Data Entry operator WRITING the invoice template = %d, want 403.\n"+
			"⛔ THIS IS THE TOKEN CHOICE FAILING. If this route were modelled as report:NAME, "+
			"migration 0011's coarse `allow display report` would hand every operator in every "+
			"tenant the power to rewrite what the company's invoices say (%s)",
			rec.Code, rec.Body.String())
	}

	// Being a write it takes the Owner level's WebAuthn step-up; the read does not.
	rec, wbody := aclRequest(t, s, http.MethodPut, tmplPath, f.ownerNoStepUp, map[string]any{
		"declaration": "Anything at all.",
	})
	if rec.Code != http.StatusForbidden || wbody["code"] != "STEPUP_REQUIRED" {
		t.Errorf("PUT with an Owner token that has NOT stepped up = %d code=%v, want 403 "+
			"STEPUP_REQUIRED: a write inherits the level's second factor (%s)",
			rec.Code, wbody["code"], rec.Body.String())
	}
	if rec, _ := aclRequest(t, s, http.MethodGet, tmplPath, f.ownerNoStepUp, nil); rec.Code != http.StatusOK {
		t.Errorf("GET with an Owner token that has not stepped up = %d, want 200: a read is a "+
			"Display and must not demand a second factor (%s)", rec.Code, rec.Body.String())
	}
}
