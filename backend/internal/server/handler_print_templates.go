package server

// handler_print_templates.go — PER-COMPANY PRINT TEMPLATES (track C).
//
// WHAT WAS ACTUALLY MISSING HERE, BECAUSE IT IS NOT WHAT IT LOOKS LIKE.
// printingdb.Store.SaveOverrides has been fully implemented and tested since
// migration 0037: a real upsert keyed on (company_id, doc_kind) with updated_at
// maintained on both paths. The READ side has been wired all along too —
// printingdb/document.go calls LoadOverrides on both document paths and
// handler_print_reports.go calls it for the report letterhead — so the printer
// has honoured overrides from the day it shipped.
//
// NOTHING COULD WRITE ONE. No route reached SaveOverrides, and the table held
// zero rows in the entire database. This file is the missing route, and that is
// the whole of the server-side work: it is NOT a templating system, and the
// three ways a company's paper changes were all already in place.
//
// ⛔ THE ONE THING THIS ROUTE MUST NOT DO IS STORE A FIELD THAT WILL NEVER PRINT.
// RenderReport reads three of the seven overridable fields and ignores the other
// four — a declaration on a Trial Balance would imply a certification nobody
// gave. A PUT carrying a declaration for the `report` kind is therefore REFUSED
// rather than stored, because storing it produces the worst outcome available:
// the operator typed it, the screen accepted it, the row holds it, and no printed
// statement will ever show it. printing.HonouredFields is the authority and
// TestPrintTemplateSurfaceGolden measures it against the renderers by rendering.
//
// ⚠️ THERE IS DELIBERATELY NO SERVER-COMPUTED "EFFECTIVE TEMPLATE" IN THE
// RESPONSE, and it was drafted and removed. It would be a THIRD implementation of
// "blank means fall back to the built-in", and the two that exist do not agree:
// invoice.go tests `invBlank`, which trims whitespace, while voucherdoc.go's
// declaration tests `d != ""`, which does not — so a declaration of one space
// prints the default on an invoice and a blank line on a delivery note. A third
// copy would be right about neither and would drift from both. The response
// carries what is STORED and what the BUILT-IN DEFAULT is, separately and
// honestly, and the screen shows the default as the placeholder in an empty box.
// That tells the operator what will print without anybody re-deriving it.

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/khata/cored/internal/printing"
	"github.com/khata/cored/internal/printing/printingdb"
)

// The sanity caps below are NOT a layout guarantee and must not be read as one.
//
// The real constraint on override length is geometric and belongs to the
// renderer: RenderInvoice refuses to print a document whose overrides do not fit
// the page and says which ones to shorten (see invoice.go's overflow message,
// which distinguishes "shorten these overrides" from "the built-in block itself
// does not fit"). That check needs a document — a party name, a line count, an
// item table — and none of that exists at the moment a template is SAVED, so it
// cannot be performed here without guessing.
//
// What these caps do is refuse the absurd, so that a paste accident is a 400 at
// save time rather than a company whose invoices stop printing until somebody
// finds this table. A value inside these caps can still overflow a crowded page,
// and the renderer's message is what an operator sees then.
const (
	maxTemplateTextLen  = 2000
	maxTemplateLines    = 20
	maxTemplateLineLen  = 200
	templateCapsMessage = "print template limits: a text field is at most 2000 characters, " +
		"a line list at most 20 lines, and a line at most 200 characters. These refuse a " +
		"paste accident; they do not promise the value fits a printed page — a document " +
		"whose overrides overflow is refused at PRINT time, with a message naming what to shorten"
)

// printTemplateReq is the PUT body. Every field is optional and an ABSENT one is
// stored as empty, i.e. "fall back to the built-in default".
//
// ⚠️ ABSENT MEANS CLEARED HERE, WHICH IS THE OPPOSITE OF handleUpdateCompany'S
// RULE, and the difference is deliberate. The features route uses pointers so an
// absent field leaves the stored value alone, because it edits ONE flag among
// many that other callers own. This route replaces a whole template: the screen
// submits the entire form, so "the declaration box is empty" has to mean the
// declaration is empty. Pointer-merge semantics would make an emptied box
// un-emptiable — the operator deletes the text, saves, and it comes back.
//
// To remove the customisation ALTOGETHER, use DELETE. A PUT of all-blank fields
// stores a row that still says "this company has a customisation"; see
// printingdb.DeleteOverrides for why the two are not the same.
type printTemplateReq struct {
	Title           string   `json:"title"`
	HeaderLines     []string `json:"header_lines"`
	FooterLines     []string `json:"footer_lines"`
	Declaration     string   `json:"declaration"`
	Terms           string   `json:"terms"`
	SignatureLabel  string   `json:"signature_label"`
	ShowBankDetails bool     `json:"show_bank_details"`
	BankLines       []string `json:"bank_lines"`
}

// printTemplateDocKind reads and validates the {docKind} path segment.
//
// It refuses an unknown kind with a 400 that NAMES THE VOCABULARY, before any
// transaction is opened. printingdb.KnownKind exists for exactly this, and the
// alternative — letting the kind reach the store — turns a typo into either a
// constraint violation or, worse, a stored row no renderer ever matches, which
// from the screen looks like a customisation that simply does not apply.
func printTemplateDocKind(w http.ResponseWriter, r *http.Request) (printing.DocKind, bool) {
	kind := printing.DocKind(chi.URLParam(r, "docKind"))
	if !printingdb.KnownKind(kind) {
		known := make([]string, 0, len(printing.AllDocKinds()))
		for _, k := range printing.AllDocKinds() {
			known = append(known, string(k))
		}
		writeError(w, http.StatusBadRequest,
			"unknown document kind "+string(kind)+": this system prints "+strings.Join(known, ", "))
		return "", false
	}
	return kind, true
}

// printTemplateBody is the response shape for all three verbs.
//
// It answers four separate questions, and collapsing any two of them is a bug an
// editing screen cannot recover from:
//
//	customised — is there a row at all? Without it the screen cannot offer to
//	             clear an override, because it does not know one exists, and it
//	             cannot tell "never asked" from "deliberately blank".
//	overrides  — what is STORED. Blank fields are blank on purpose.
//	defaults   — what PRINTS when a field is blank, so an empty box can show the
//	             real text as its placeholder instead of reading as "this company
//	             has no declaration".
//	honours    — which fields this kind's renderer actually reads, so the form
//	             does not offer a control whose value would never appear.
type printTemplateBody struct {
	DocKind    string                   `json:"doc_kind"`
	Customised bool                     `json:"customised"`
	Honours    []printing.TemplateField `json:"honours"`
	Defaults   printTemplateDefaults    `json:"defaults"`
	// DynamicTitle: this kind honours a title override but has no fixed default,
	// because the default is the document's own name. True only for `report`,
	// whose title is whichever of the thirty statements is being printed.
	DynamicTitle bool             `json:"default_title_is_the_documents_own_name"`
	Overrides    printTemplateReq `json:"overrides"`
	// Cleared is present only on a DELETE, and says whether there was anything to
	// clear. A clear of an absent override is a success, not a 404: the company
	// ends up on the built-in layout either way, which is what was asked for.
	Cleared *bool `json:"cleared,omitempty"`
}

type printTemplateDefaults struct {
	Title          string `json:"title"`
	Declaration    string `json:"declaration"`
	SignatureLabel string `json:"signature_label"`
}

// printTemplateResponse assembles the body from the stored row plus the
// renderer's own answers about defaults and honoured fields.
func printTemplateResponse(kind printing.DocKind, o printing.TemplateOverrides, customised bool) printTemplateBody {
	honours := printing.HonouredFields(kind)
	if honours == nil {
		honours = []printing.TemplateField{}
	}
	return printTemplateBody{
		DocKind:    string(kind),
		Customised: customised,
		Honours:    honours,
		Defaults: printTemplateDefaults{
			Title:          printing.DefaultTitle(kind),
			Declaration:    printing.DefaultDeclaration(kind),
			SignatureLabel: printing.DefaultSignatureLabel(kind),
		},
		DynamicTitle: printing.Honours(kind, printing.FieldTitle) && printing.DefaultTitle(kind) == "",
		Overrides: printTemplateReq{
			Title:           o.Title,
			HeaderLines:     o.HeaderLines,
			FooterLines:     o.FooterLines,
			Declaration:     o.Declaration,
			Terms:           o.Terms,
			SignatureLabel:  o.SignatureLabel,
			ShowBankDetails: o.ShowBankDetails,
			BankLines:       o.BankLines,
		},
	}
}

// handleGetPrintTemplate serves the company's template for one document kind.
func (s *Server) handleGetPrintTemplate(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
	kind, ok := printTemplateDocKind(w, r)
	if !ok {
		return
	}
	o, customised, err := s.printStore.LoadOverridesRow(ctx, companyID, kind)
	if err != nil {
		writePrintTemplateError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, printTemplateResponse(kind, o, customised))
}

// handleSavePrintTemplate upserts the company's template for one document kind.
func (s *Server) handleSavePrintTemplate(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
	kind, ok := printTemplateDocKind(w, r)
	if !ok {
		return
	}
	var req printTemplateReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if msg, bad := printTemplateFaults(kind, req); bad {
		writeError(w, http.StatusBadRequest, msg)
		return
	}

	o := printing.TemplateOverrides{
		Title:           req.Title,
		HeaderLines:     req.HeaderLines,
		FooterLines:     req.FooterLines,
		Declaration:     req.Declaration,
		Terms:           req.Terms,
		SignatureLabel:  req.SignatureLabel,
		ShowBankDetails: req.ShowBankDetails,
		BankLines:       req.BankLines,
	}
	if err := s.printStore.SaveOverrides(ctx, companyID, kind, o); err != nil {
		writePrintTemplateError(w, err)
		return
	}

	// READ BACK, rather than echoing the request. The response is what the screen
	// re-renders from, and the row is what the printer will read: echoing the
	// request would hide a column that silently dropped a value — the failure this
	// route's own end-to-end test exists to catch.
	stored, customised, err := s.printStore.LoadOverridesRow(ctx, companyID, kind)
	if err != nil {
		writePrintTemplateError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, printTemplateResponse(kind, stored, customised))
}

// handleClearPrintTemplate removes the company's template for one document kind,
// returning it to the built-in layout.
//
// ⛔ THIS ROUTE IS WHY THE FEATURE IS USABLE AT ALL. Without it a company that
// sets a declaration once can never get back to the default: an all-blank PUT
// stores a row that still reads as a customisation, and there would be no way to
// say "we never mind after all". A one-way door is not a setting.
func (s *Server) handleClearPrintTemplate(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
	kind, ok := printTemplateDocKind(w, r)
	if !ok {
		return
	}
	cleared, err := s.printStore.DeleteOverrides(ctx, companyID, kind)
	if err != nil {
		writePrintTemplateError(w, err)
		return
	}
	o, customised, err := s.printStore.LoadOverridesRow(ctx, companyID, kind)
	if err != nil {
		writePrintTemplateError(w, err)
		return
	}
	body := printTemplateResponse(kind, o, customised)
	body.Cleared = &cleared
	writeJSON(w, http.StatusOK, body)
}

// printTemplateFaults validates a PUT body against the kind it is being stored
// for, returning the message to refuse it with.
//
// ⛔ THE FIRST CHECK IS THE ONE THAT MATTERS: a field this kind's renderer does
// not read, carrying CONTENT, is refused. Only content is refused, not presence —
// a screen that submits its whole form shape with the unused boxes empty is
// behaving correctly and must not get a 400 for it. What must not be accepted is
// a VALUE that has been typed, acknowledged and stored and will never appear on
// paper.
func printTemplateFaults(kind printing.DocKind, req printTemplateReq) (string, bool) {
	type textField struct {
		field printing.TemplateField
		name  string
		value string
	}
	type lineField struct {
		field printing.TemplateField
		name  string
		value []string
	}
	texts := []textField{
		{printing.FieldTitle, "title", req.Title},
		{printing.FieldDeclaration, "declaration", req.Declaration},
		{printing.FieldTerms, "terms", req.Terms},
		{printing.FieldSignatureLabel, "signature_label", req.SignatureLabel},
	}
	lines := []lineField{
		{printing.FieldHeaderLines, "header_lines", req.HeaderLines},
		{printing.FieldFooterLines, "footer_lines", req.FooterLines},
		{printing.FieldBankDetails, "bank_lines", req.BankLines},
	}

	unhonoured := func(field printing.TemplateField, name string) (string, bool) {
		return "the " + string(kind) + " layout does not print " + name + ", so this would be " +
			"stored and never appear. Fields this document honours: " +
			printTemplateHonouredList(kind), true
	}

	for _, t := range texts {
		if strings.TrimSpace(t.value) != "" && !printing.Honours(kind, t.field) {
			return unhonoured(t.field, t.name)
		}
		if len(t.value) > maxTemplateTextLen {
			return t.name + " is too long. " + templateCapsMessage, true
		}
	}
	for _, l := range lines {
		if len(l.value) > 0 && !printing.Honours(kind, l.field) {
			return unhonoured(l.field, l.name)
		}
		if len(l.value) > maxTemplateLines {
			return l.name + " has too many lines. " + templateCapsMessage, true
		}
		for _, line := range l.value {
			if len(line) > maxTemplateLineLen {
				return "a line in " + l.name + " is too long. " + templateCapsMessage, true
			}
		}
	}
	// show_bank_details rides with bank_lines: it is the same control, and the
	// renderers all test the flag AND the lines together, so the flag alone on a
	// kind that prints no bank block is the same silent lie.
	if req.ShowBankDetails && !printing.Honours(kind, printing.FieldBankDetails) {
		return unhonoured(printing.FieldBankDetails, "bank details")
	}
	return "", false
}

// printTemplateHonouredList renders the honoured fields for an error message.
func printTemplateHonouredList(kind printing.DocKind) string {
	fields := printing.HonouredFields(kind)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		out = append(out, string(f))
	}
	if len(out) == 0 {
		return "(none)"
	}
	return strings.Join(out, ", ")
}

// writePrintTemplateError maps the store's errors onto status codes.
//
// It does NOT reuse writePrintError. That mapper is about printing a DOCUMENT —
// vouchers that do not exist, particulars the books cannot supply, Rule 46
// faults — and none of those can arise here. Sharing it would mean a template
// route could answer with a message about a missing voucher.
func writePrintTemplateError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, printingdb.ErrUnknownDocKind):
		// Defence in depth: printTemplateDocKind already refused these with a 400
		// that names the vocabulary, so reaching here means the two disagree.
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, printingdb.ErrNoCompany):
		// A forgotten tenant scope, reported as the bad request it is. "Denied by
		// RLS" and "this company has no override" are indistinguishable in a
		// result set and only one of them is a bug.
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "print template: "+err.Error())
	}
}
