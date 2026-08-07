package statutory

// einvoice_api.go — statutoryd's HTTP SURFACE, lifted out of cmd/statutoryd so
// it is importable.
//
// ===========================================================================
// ⛔⛔ WHY THIS MOVED OUT OF package main
// ===========================================================================
// It used to be `handleEInvoice` in cmd/statutoryd/main.go, where it had NO TEST
// (main_test.go covers only newStepStore) and — worse — could not have one that
// mattered, because `package main` is not importable. The moment cored acquired
// a PROXY route to this endpoint that became a real defect risk rather than a
// tidiness complaint: cored's acceptance test has to drive the REAL statutoryd
// handler or it proves nothing about the contract between them. A test that
// stands up a hand-rolled statutoryd look-alike passes happily while the actual
// daemon ignores a field cored sends, and the failure surfaces in production as
// `[ItemList: 0 items, minimum 1]`.
//
// So the handler lives here, cmd/statutoryd/main.go is pure wiring, and
// internal/server's e-invoice test mounts THIS type. One implementation, driven
// from both ends.
//
// ⚠️ This adds no sibling import: EInvoiceAPI reaches chi and net/http and
// nothing else. internal/statutory's allowed set stays {internal/statutory/rules}.

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/khata/cored/internal/statutory/rules"
)

// The wire contract is defined ONCE, in the pure leaf, and re-exported here by
// ALIAS so both deployables name the same type. See rules/einvoice_wire.go for
// why a second struct in cored would be the drift this design exists to make
// impossible.
type (
	// EInvoiceRequest is the e-invoice POST body. See rules.EInvoiceRequest.
	EInvoiceRequest = rules.EInvoiceRequest
	// EInvoiceResponse is the e-invoice 200 body. See rules.EInvoiceResponse.
	EInvoiceResponse = rules.EInvoiceResponse
)

// EInvoiceRoute is the pattern the e-invoice endpoint is mounted at, relative
// to the router it is mounted on. Named rather than written twice so the daemon
// and every test that drives it cannot disagree about the path.
const EInvoiceRoute = "/companies/{companyID}/vouchers/{voucherID}/e-invoice"

// EInvoiceAPI is statutoryd's e-invoice endpoint over the durable workflow.
type EInvoiceAPI struct {
	Store    StepStore
	Provider ProviderAdapter
	Cored    CoredClient
}

// Mount registers the e-invoice route on r.
func (a *EInvoiceAPI) Mount(r chi.Router) {
	r.Post(EInvoiceRoute, a.HandleEInvoice)
}

// writeAPIJSON writes v as JSON with the given status.
func writeAPIJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// HandleEInvoice fetches the voucher's amounts from cored, overlays the
// caller-supplied identity and lines, and runs the durable IRN workflow.
//
// # ⛔ THE STATUS MAPPING IS LOAD-BEARING AND IS THE REASON THIS HAS A TEST
//
//	400 the body is not JSON.
//	502 cored could not be reached or refused. ⚠️ A cored refusal is usually a
//	    401 on a call THIS SERVICE made, i.e. an expired or invalidated service
//	    token (every cored restart mints a new ephemeral signing key). The
//	    message carries cored's own text so the proxy upstream can say so.
//	422 IsPermanent(err): a schema violation or a permanent IRP reject.
//	    Re-running deterministically fails again, so the CALLER must fix the
//	    voucher. Retrying is pointless and the status says so.
//	503 anything else: transient, and the workflow is RESUMABLE by design — the
//	    memoized `generate` step means a retry never mints a second IRN.
//
// Collapsing 422 and 503 into one error status is the tempting simplification
// and it is wrong in the expensive direction: a proxy that retries a permanent
// reject hammers a government API, and a proxy that gives up on a transient
// fault abandons a workflow that would have completed.
func (a *EInvoiceAPI) HandleEInvoice(w http.ResponseWriter, r *http.Request) {
	companyID := chi.URLParam(r, "companyID")
	voucherID := chi.URLParam(r, "voucherID")

	var req EInvoiceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	ctx := r.Context()
	snap, err := a.Cored.GetVoucher(ctx, companyID, voucherID)
	if err != nil {
		writeAPIJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}

	snap = req.Overlay(snap)

	res, err := GenerateIRNWorkflow(ctx, a.Store, a.Provider, a.Cored, companyID, voucherID, snap)
	if err != nil {
		if IsPermanent(err) {
			writeAPIJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
			return
		}
		// Transient: the workflow is resumable; surface 503 so the caller retries.
		writeAPIJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}

	writeAPIJSON(w, http.StatusOK, EInvoiceResponse{
		IRN:             res.IRN,
		AckNo:           res.AckNo,
		Status:          res.Status,
		PinnedVoucherNo: snap.VoucherNo,
	})
}
