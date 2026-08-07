package server

// HTTP surface for the e-invoice / IRN, and cored's PROXY into statutoryd.
//
// ===========================================================================
// ⛔⛔ THE ROUTE IS FLAT (/e-invoices) AND NOT NESTED UNDER /vouchers, AND THAT
// IS FORCED RATHER THAN CHOSEN
// ===========================================================================
// Exactly the trap handler_ewaybill.go's header documents, and it applies here
// verbatim: routeAction's voucher arm is
// `method == POST && strings.HasPrefix(rest, "/vouchers/")`, and everything
// after that prefix that is not `{id}/external-reference` is read as the voucher
// CLASS. A POST to /vouchers/{id}/e-invoice would map to a request to create a
// voucher of class "{id}/e-invoice" — a class no vocabulary knows, i.e.
// default-deny for the Owner too, presenting as an unexplained 403.
//
// So the resource lives at its own top-level path and names the voucher in the
// body (POST) or the path (GET).
//
// ⚠️ statutoryd's OWN endpoint is nested — POST
// /companies/{c}/vouchers/{v}/e-invoice — and that is fine: statutoryd has no
// routeAction, no PDP and no voucher classes. The two paths differ on purpose.
//
// ===========================================================================
// ONE BARE RESOURCE TOKEN, `e-invoice`, AND NO ACL MIGRATION
// ===========================================================================
// HANDOFF §6.1: the seeded Data Entry level has default_access='none' and every
// seeded rule names a specific token, so a token never mentioned is DENIED and
// no migration is needed to deny it. `e-way-bill` shipped exactly this way and
// consumed no migration number.
//
// State the consequence plainly, as the T3 and T5 arms do: DATA ENTRY CANNOT
// RAISE OR SEE AN e-INVOICE until an administrator writes
// `allow create e-invoice`.
//
// ⛔ `e-invoice` IS NOT IN authz.KnownReportNames AND MUST NOT BE ADDED.
// server.go loops that list and AUTO-MOUNTS GET /print/reports/<name>; a name
// with no reportName() case is default-DENY for everyone including the Owner,
// which already shipped once for reconciliation-queue. There is no e-invoice
// REPORT in this slice.

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/khata/cored/internal/einvoice"
	"github.com/khata/cored/internal/platform/db"
)

// eInvoiceReq is the body of POST .../e-invoices.
//
// ⛔ IT CARRIES ONLY THE VOUCHER, AND THAT IS THE WHOLE POINT OF THE PROXY.
// statutoryd's own request demands seller and buyer GSTIN, legal name and state
// code plus the entire NIC item list, because statutoryd cannot read cored's
// masters. cored CAN. Every one of those fields is derived from the `company`
// row, the party `ledger` row and the voucher's own GST and stock lines, so a
// browser that had to supply them would be re-typing the books — and a browser
// that mistyped one would put a wrong GSTIN on a document with a government
// reference number.
type eInvoiceReq struct {
	VoucherID string `json:"voucher_id"`
}

// postEInvoice raises the e-invoice for one voucher through statutoryd.
//
// ⛔⛔ THIS HANDLER OPENS NO TRANSACTION OF ITS OWN AND MUST NEVER ACQUIRE ONE.
// The call graph is cored -> statutoryd -> cored: statutoryd calls back in for
// the voucher and again for the freeze hook while this request is still in
// flight. internal/einvoice's Raise commits its read phase before calling out;
// wrapping this handler in a transaction would put the connection back and
// re-create the deadlock the two-phase split exists to prevent.
func (s *Server) postEInvoice() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, ok := s.scopeCtx(w, r)
		if !ok {
			return
		}
		var req eInvoiceReq
		if !decodeJSON(w, r, &req) {
			return
		}
		voucherID, err := optionalUUID(req.VoucherID)
		if err != nil || voucherID == uuid.Nil {
			// ⛔ The all-zeros UUID PARSES (HANDOFF §9.5), so `err == nil` is not
			// enough on its own.
			writeError(w, http.StatusBadRequest,
				"voucher_id must be the uuid of the invoice the e-invoice is raised for")
			return
		}

		res, err := s.einvoice.Raise(ctx, einvoice.Input{VoucherID: voucherID})
		if err != nil {
			writeEInvoiceError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"voucher_id": res.VoucherID.String(),
			"voucher_no": res.VoucherNo,

			"irn":    res.IRN,
			"ack_no": res.AckNo,
			"status": res.Status,

			// ⛔ THE PINNED NUMBER TRAVELS, and it is not decoration. The freeze
			// hook has just made this voucher un-alterable and un-deletable, and
			// this is the number the IRN refers to. A screen that does not show it
			// leaves the operator to discover the freeze by being refused an edit.
			"pinned_voucher_no": res.PinnedVoucherNo,
		})
	}
}

// getEInvoice returns the IRN pinned onto one voucher.
func (s *Server) getEInvoice() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, ok := s.scopeCtx(w, r)
		if !ok {
			return
		}
		voucherID, err := optionalUUID(chi.URLParam(r, "voucherID"))
		if err != nil || voucherID == uuid.Nil {
			writeError(w, http.StatusBadRequest, "voucherID must be a uuid")
			return
		}
		ref, err := s.einvoice.Get(ctx, voucherID)
		if err != nil {
			writeEInvoiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"voucher_id":        ref.VoucherID.String(),
			"voucher_no":        ref.VoucherNo,
			"irn":               ref.IRN,
			"pinned_voucher_no": ref.PinnedVoucherNo,
			"created_by":        ref.CreatedBy,
			"created_at":        ref.CreatedAt,
			// ⚠️ NO `ack_no` KEY HERE, DELIBERATELY, AND ITS ABSENCE IS THE HONEST
			// ANSWER. voucher_external_reference has no column for the NIC
			// acknowledgement number; it exists only in the raise response. A key
			// carrying "" would read as "this invoice has no ack number", which is
			// a claim about the IRP rather than about this schema.
		})
	}
}

// writeEInvoiceError maps the domain's sentinels and statutoryd's classification
// onto status codes.
//
// ⛔ THE THREE FAILURE FAMILIES ARE KEPT APART BECAUSE THEY ARE FIXED BY THREE
// DIFFERENT PEOPLE, and collapsing them is how an operator ends up staring at a
// bare 502:
//
//	422 THE VOUCHER IS WRONG — the accountant fixes it. Our own refusals (the
//	    voucher is not an outward SALES invoice, no tax lines, mismatched lines,
//	    an item master with no unit of measure, an undescribable service line) and
//	    statutoryd's permanent ones (NIC schema violations, a permanent IRP
//	    reject) both land here. Retrying without changing the voucher cannot help.
//
//	    ⚠️ "THE VOUCHER IS WRONG" INCLUDES "THIS IS THE WRONG VOUCHER", which is
//	    the class refusal: a Purchase can be a perfectly good purchase and still
//	    not be e-invoiceable. The message names the class, so the 422 is not read
//	    as "your purchase is malformed".
//	503 SOMETHING IS DOWN OR SLOW — whoever runs the stack fixes it, and the
//	    operator should simply try again. statutoryd not answering, statutoryd's
//	    service token dead, or statutoryd reporting a transient IRP fault. ⛔ The
//	    IRN workflow is RESUMABLE and its `generate` step is memoized, so a retry
//	    after a transient fault CANNOT mint a second IRN — which is what makes
//	    "try again" safe advice to print.
//	404 no such voucher.
func writeEInvoiceError(w http.ResponseWriter, err error) {
	var statutoryd *einvoice.StatutorydError

	switch {
	case errors.Is(err, einvoice.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, einvoice.ErrNotAnOutwardSalesInvoice),
		errors.Is(err, einvoice.ErrNoTaxLines),
		errors.Is(err, einvoice.ErrLineMismatch),
		errors.Is(err, einvoice.ErrNoUnitOfMeasure),
		// ⚠️ A REFUSAL, NOT A CRASH. This one describes an arithmetic the books
		// and NIC's two-decimal item block cannot both satisfy, so it is
		// something about THIS VOUCHER that the operator can act on — 422, like
		// its five siblings. Left off this list it fell through to `default` and
		// answered 500, which reads as "the server is broken" for a document
		// this build correctly declined to misstate.
		errors.Is(err, einvoice.ErrPrecisionNotExpressible),
		errors.Is(err, einvoice.ErrLineNotDescribable):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, einvoice.ErrStatutorydUnreachable),
		errors.Is(err, einvoice.ErrStatutorydTimedOut),
		errors.Is(err, einvoice.ErrServiceTokenRejected):
		// 503 and not 502: the request is RETRYABLE once the plane is back, and
		// the message already says what to do about it. A 502 tells a browser the
		// upstream misbehaved and tells the operator nothing.
		writeError(w, http.StatusServiceUnavailable, err.Error())
	case errors.As(err, &statutoryd):
		if statutoryd.Permanent {
			writeError(w, http.StatusUnprocessableEntity, statutoryd.Message)
			return
		}
		writeError(w, http.StatusServiceUnavailable, statutoryd.Message)
	case errors.Is(err, db.ErrNoCompany):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}
