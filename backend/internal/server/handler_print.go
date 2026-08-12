package server

// handler_print.go — the PRINT surface (track T1, wave W1).
//
//	GET /v1/companies/{companyID}/print/vouchers/{class}/{voucherID}
//	GET /v1/companies/{companyID}/print/reports/{reportName}   (handler_print_reports.go)
//
// Both answer `Content-Type: application/pdf` with a Content-Disposition
// filename, and both are READS.
//
// PRINT IS NOT A WRITE, AND THAT IS A DECISION WITH TEETH. authz.Print's
// IsWrite() is false, so neither route inherits the back-dated write cutoff
// (voucherDateFromRequest returns nil for a non-write) nor the Owner level's
// WebAuthn step-up (both gate on Action.IsWrite()). An operator asked for a
// second factor to reprint the invoice they keyed five minutes ago prints it
// from a spreadsheet instead, and then the books and the paper disagree.
// TestAnOwnerWhoHasNotSteppedUpCanStillPrint pins it, with a control proving the
// step-up is genuinely armed for the same token on a write.
//
// ===========================================================================
// THE PRIVILEGE-ESCALATION HOLE THIS ROUTE SHAPE CREATES
// ===========================================================================
// The PDP decides from the class IN THE PATH, before a single row is read —
// that is how POST /vouchers/{class} has always worked, and it is what lets an
// administrator write `allow print sales` / `disallow print payroll` at all.
// The consequence is that the id in the path is UNCHECKED at decision time. A
// level allowed `print sales` and refused `print payroll` could otherwise ask
// for /print/vouchers/sales/<the id of a PAYROLL voucher> and, if this handler
// printed whatever it found, be handed the wage bill it was explicitly refused.
//
// So the FIRST thing this handler does — before any document is assembled — is
// read the voucher's STORED class (printingdb.Store.VoucherClass, which reads
// payload->>'class' on the current version, i.e. what was signed) and refuse
// any mismatch.
//
// IT REFUSES WITH 404 AND NOT 403, and the body is the SAME body a genuinely
// non-existent id gets. A 403 here would confirm that a voucher with that id
// exists in this company, which is precisely the fact the refusal is protecting:
// an operator who can enumerate payroll voucher ids can tell how many pay runs
// there have been and when. See printVoucherNotFoundMsg.

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/printing"
	"github.com/khata/cored/internal/printing/printingdb"
)

// printVoucherNotFoundMsg is the ONE body a print request gets for "there is no
// document here for you". It is deliberately used for THREE different
// situations, which the caller therefore cannot tell apart:
//
//  1. no voucher with that id in this company;
//  2. a TOMBSTONED voucher (printingdb never prints a deleted voucher — a fresh
//     copy of a cancelled invoice looks exactly like a live one);
//  3. a voucher whose STORED class is not the class in the path.
//
// (3) is the escalation refusal, and it is only a refusal if it is
// indistinguishable from (1). The message therefore names no class, no id and no
// reason — a caller probing /print/vouchers/sales/<uuid> learns nothing about
// what that uuid is.
const printVoucherNotFoundMsg = "no printable voucher with that id in this company"

// handlePrintVoucher renders one voucher's document as a PDF.
func (s *Server) handlePrintVoucher(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}

	// The class is a LITERAL path segment (routes() registers one route per
	// class, drawn from authz.KnownVoucherClasses), so it is read back out of the
	// path rather than from a chi param — and it is read with the same splitPath
	// routeAction used to authorize it, so the class this handler checks against
	// the books is the class the PDP made its decision from. Two different
	// notions of "the class in the path" is exactly how the check below would
	// come to guard a different request than the one that was authorized.
	segs := splitPath(routeSuffix(r))
	if len(segs) != 4 || segs[0] != "print" || segs[1] != "vouchers" {
		writeError(w, http.StatusNotFound, printVoucherNotFoundMsg)
		return
	}
	pathClass := segs[2]

	voucherID, err := uuid.Parse(chi.URLParam(r, "voucherID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid voucher id")
		return
	}

	// (1) THE ESCALATION GATE. See the file header.
	stored, err := s.printStore.VoucherClass(ctx, companyID, voucherID)
	if err != nil {
		writePrintError(w, err)
		return
	}
	if !sameVoucherClass(pathClass, stored) {
		writeError(w, http.StatusNotFound, printVoucherNotFoundMsg)
		return
	}

	// (2) Assemble and render.
	pdf, number, err := s.printVoucherPDF(ctx, companyID, voucherID)
	if err != nil {
		writePrintError(w, err)
		return
	}
	// THE CLASS, THEN THE ONE IDENTITY THAT NAMES THE DOCUMENT.
	//
	// ⛔ THE UUID IS A FALLBACK, NOT A SUFFIX, and it was being appended to every
	// filename — "Sales-INV-2026-0041-0f0b4c31-....pdf". pdfFilename JOINS the
	// parts it is given (that is right: the report route wants both its name and
	// its date), so the falling-back is the CALLER'S job and it was never done.
	// The consequence is not cosmetic: an operator filing a folder of invoices
	// reads the number, and 36 characters of machine identity after it push the
	// number out of every truncated column a file manager, a mail client and an
	// accountant's inbox render it in.
	//
	// The id is still there for the voucher that has NO number — an unnumbered
	// voucher would otherwise download as "Sales.pdf", and the second one would
	// silently overwrite the first in the download folder.
	name := number
	if strings.TrimSpace(name) == "" {
		name = voucherID.String()
	}
	writePDF(w, pdfFilename(pathClass, name), pdf)
}

// sameVoucherClass compares the kebab-case PATH segment against the STORED,
// title-cased class ("Sales", "Credit Note", "Physical Stock").
//
// The comparison is normalising rather than exact because the two vocabularies
// are spelled differently on purpose: internal/ledger's Class constants are
// human strings that end up in the hashed payload and on every report, while the
// path segment is a URL token and also the ACL resource token. Normalising both
// to lower-kebab is the only comparison under which `credit-note` and
// `Credit Note` are the same authority — and if they were ever NOT the same
// authority, every credit note in the product would 404 on print.
func sameVoucherClass(pathClass, storedClass string) bool {
	norm := func(s string) string {
		s = strings.ToLower(strings.TrimSpace(s))
		s = strings.ReplaceAll(s, " ", "-")
		s = strings.ReplaceAll(s, "_", "-")
		return s
	}
	p, st := norm(pathClass), norm(storedClass)
	// An empty stored class is not a match for anything. A version whose payload
	// records no class cannot be authorized against a path class, and treating ""
	// as "matches whatever you asked for" would turn a data fault into an
	// authorization bypass.
	return st != "" && p == st
}

// printVoucherPDF loads the voucher's document and renders it.
//
// WHY IT ASKS FOR AN INVOICE FIRST AND BRANCHES ON THE ERROR, rather than
// mapping the class to a loader here. printingdb already owns the class ->
// document decision (docKindForClass, with a written reason per class for why
// Purchase, Contra, Journal, Payroll, Receipt Note, Stock Journal and Physical
// Stock print nothing at all). A copy of that table in this package would be a
// second place for it to be wrong, and the failure mode is silent: a class moved
// from one loader to the other in printingdb would keep working here and produce
// the WRONG document. ErrNotAnInvoice exists precisely so a caller can dispatch
// without owning the mapping.
func (s *Server) printVoucherPDF(ctx context.Context, companyID, voucherID uuid.UUID) ([]byte, string, error) {
	inv, err := s.printStore.LoadInvoice(ctx, companyID, voucherID)
	switch {
	case err == nil:
		pdf, rerr := printing.RenderInvoice(inv)
		return pdf, inv.Number, rerr
	case !errors.Is(err, printingdb.ErrNotAnInvoice):
		return nil, "", err
	}

	doc, err := s.printStore.LoadVoucherDoc(ctx, companyID, voucherID)
	if err != nil {
		return nil, "", err
	}
	pdf, rerr := printing.RenderVoucherDoc(doc)
	return pdf, doc.Number, rerr
}

// writePrintError maps every error the print path can raise onto a status.
//
// THE THREE-WAY SPLIT IS THE POINT, because the three call for different people:
//
//	4xx CLIENT/DATA — the request named something that is not a printable
//	  document (400), or the BOOKS are missing a particular the paper cannot go
//	  out without (422). An operator fixes these, and the message already names
//	  the field, the master record and the rule, so it is surfaced verbatim.
//	404 — see printVoucherNotFoundMsg. Never a 403, never a message that
//	  confirms the voucher exists.
//	500 — a LAYOUT refusal (a figure that does not fit its column, a row wider
//	  than the table). Those are caller/configuration faults in this package or
//	  in a company's template weights, not data conditions, and they are logged
//	  verbatim because the message names the figure, the column and the row.
//	  NOTHING falls back to a partial or column-dropped PDF: the whole reason
//	  internal/printing refuses is that no wrong figure reaches paper.
func writePrintError(w http.ResponseWriter, err error) {
	var rule46 *printing.Rule46Error
	var encoding *printing.EncodingError

	switch {
	// Nothing to print for you — the id, a tombstone, or a class mismatch.
	case errors.Is(err, printingdb.ErrVoucherNotFound):
		writeError(w, http.StatusNotFound, printVoucherNotFoundMsg)

	// A forgotten tenant scope. Reported as the bad request it is rather than as
	// an empty document: "denied by RLS" and "this company has nothing" are
	// indistinguishable in a result set and only one of them is a bug.
	case errors.Is(err, printingdb.ErrNoCompany), errors.Is(err, db.ErrNoCompany):
		writeError(w, http.StatusBadRequest, err.Error())

	// This class prints no document, or prints the OTHER kind of document. The
	// message names the class, so it is surfaced verbatim: "Payroll has no
	// printable document" is an answer, where a bare 400 is a support ticket.
	case errors.Is(err, printingdb.ErrNoDocumentForClass),
		errors.Is(err, printingdb.ErrNotAnInvoice),
		errors.Is(err, printingdb.ErrNotAVoucherDoc),
		errors.Is(err, printing.ErrUnsupportedVoucherKind):
		writeError(w, http.StatusBadRequest, err.Error())

	// The BOOKS cannot supply a mandatory particular. 422, not 400: the request
	// was well formed and the data is not. printingdb's message names the master
	// record to open and whether the fix is a keystroke or a migration.
	case errors.Is(err, printingdb.ErrMissingParticular),
		errors.Is(err, printing.ErrVoucherParticularMissing):
		writeError(w, http.StatusUnprocessableEntity, err.Error())

	// Rule 46 / Rule 49 completeness. EVERY missing field goes out at once, in a
	// machine-readable list beside the prose, because the alternative is a
	// conversation: supply the HSN, be told about the unit, supply the unit, be
	// told about the place of supply. Three round trips through a voucher screen
	// is how an invoice ends up issued from a spreadsheet.
	case errors.As(err, &rule46):
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":          rule46.Error(),
			"missing_fields": rule46.Fields(),
		})

	// A bill of supply carrying tax is the WRONG DOCUMENT, not an incomplete
	// one, which is why it does not share the message above.
	case errors.Is(err, printing.ErrTaxOnBillOfSupply):
		writeError(w, http.StatusBadRequest, err.Error())

	// A name with no WinAnsi code point. 422 and not 500: the document cannot be
	// printed in the base-14 fonts this writer uses, and the fix is a
	// transliteration in the master — a real answer an operator can act on.
	case errors.As(err, &encoding):
		writeError(w, http.StatusUnprocessableEntity, err.Error())

	// LAYOUT refusals. Logged verbatim — the text names the figure and the column.
	case errors.Is(err, printing.ErrReportHasNoColumns),
		errors.Is(err, printing.ErrRowWiderThanTable),
		errors.Is(err, printing.ErrFigureColumnTooNarrow),
		errors.Is(err, printing.ErrLabelColumnTooNarrow),
		errors.Is(err, printing.ErrNoPages):
		log.Printf("print: the document was REFUSED rather than printed wrong: %v", err)
		writeError(w, http.StatusInternalServerError, err.Error())

	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

// writePDF writes the finished document.
func writePDF(w http.ResponseWriter, filename string, body []byte) {
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	// A PDF is not HTML. Without nosniff a browser is free to sniff the body,
	// and the body contains party names and narrations that came from a user.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// pdfFilename JOINS the parts given, in order, skipping any that is empty.
//
// ⚠️ IT DOES NOT FALL BACK BETWEEN THEM, whatever this comment once said — it
// said "the FIRST non-empty ones", and a reader who believed it wrote
// pdfFilename(class, number, voucherID) expecting the id to be used only when
// the number was missing. Every printed voucher then carried 36 characters of
// UUID after its number. Joining is the RIGHT behaviour for the report route,
// which wants its name AND its date; a caller that wants a fallback picks the
// part first (see handlePrintVoucher).
//
// THE SANITISATION IS LOAD-BEARING AND NOT COSMETIC. A voucher number is user
// text: Tally invoice series routinely contain '/' ("INV/2026/0041"), and a
// carriage return or a double quote in it would break out of the
// Content-Disposition header value and let a voucher number inject a response
// header. So every rune outside [A-Za-z0-9._-] becomes '-', runs of '-' are
// collapsed, and the result is capped. What a user sees is a tidy filename; what
// this actually is, is header-injection defence.
func pdfFilename(parts ...string) string {
	var b strings.Builder
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('-')
		}
		b.WriteString(p)
	}
	safe := make([]byte, 0, b.Len()+4)
	prevDash := false
	for _, r := range b.String() {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-'
		if !ok {
			r = '-'
		}
		if r == '-' {
			if prevDash {
				continue
			}
			prevDash = true
		} else {
			prevDash = false
		}
		safe = append(safe, byte(r))
		if len(safe) >= 96 {
			break
		}
	}
	name := strings.Trim(string(safe), "-.")
	if name == "" {
		name = "document"
	}
	return name + ".pdf"
}
