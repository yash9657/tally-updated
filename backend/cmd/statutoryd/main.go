// Command statutoryd is the IRN e-invoice generation plane — a thin, separate
// deployable from cored. It runs the durable IRN workflow against an in-memory
// StepStore, a deterministic FakeProvider, and an HTTPCoredClient pointed at a
// running cored, calling cored's freeze hook at IRN time.
//
// ENV:
//
//	CORED_BASE_URL  cored base URL (default http://localhost:8087)
//	PORT            HTTP listen port (default 8091)
//
// Endpoints:
//
//	POST /companies/{companyID}/vouchers/{voucherID}/e-invoice
//	  body: seller/buyer identity (gstins, legal names, state codes) + optional
//	  line/amount overrides. Fetches the voucher amounts from cored, runs
//	  GenerateIRNWorkflow, returns { irn, ack_no, status, pinned_voucher_no }.
//	GET /healthz -> {status:"ok", service:"statutoryd"}
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"

	"github.com/khata/cored/internal/statutory"
)

const (
	defaultCoredBaseURL = "http://localhost:8087"
	defaultPort         = "8091"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

type server struct {
	store statutory.StepStore
	prov  statutory.ProviderAdapter
	cored statutory.CoredClient
}

// eInvoiceRequest carries the seller/buyer identity (not held by cored's ledger)
// plus optional snapshot lines/amount overrides for the e-invoice.
type eInvoiceRequest struct {
	SellerGstin     string `json:"seller_gstin"`
	SellerLegalName string `json:"seller_legal_name"`
	SellerStateCode string `json:"seller_state_code"`
	BuyerGstin      string `json:"buyer_gstin"`
	BuyerLegalName  string `json:"buyer_legal_name"`
	BuyerStateCode  string `json:"buyer_state_code"`

	Lines []statutory.SnapshotLine `json:"lines,omitempty"`

	// Optional amount overrides; when blank the values fetched from cored are used.
	Taxable      string `json:"taxable,omitempty"`
	CGST         string `json:"cgst,omitempty"`
	SGST         string `json:"sgst,omitempty"`
	IGST         string `json:"igst,omitempty"`
	InvoiceTotal string `json:"invoice_total,omitempty"`
}

type eInvoiceResponse struct {
	IRN             string `json:"irn"`
	AckNo           string `json:"ack_no"`
	Status          string `json:"status"`
	PinnedVoucherNo string `json:"pinned_voucher_no"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *server) handleEInvoice(w http.ResponseWriter, r *http.Request) {
	companyID := chi.URLParam(r, "companyID")
	voucherID := chi.URLParam(r, "voucherID")

	var req eInvoiceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	ctx := r.Context()
	snap, err := s.cored.GetVoucher(ctx, companyID, voucherID)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}

	// Overlay caller-supplied identity (and optional amount/line overrides).
	snap.SellerGstin = req.SellerGstin
	snap.SellerLegalName = req.SellerLegalName
	snap.SellerStateCode = req.SellerStateCode
	snap.BuyerGstin = req.BuyerGstin
	snap.BuyerLegalName = req.BuyerLegalName
	snap.BuyerStateCode = req.BuyerStateCode
	if len(req.Lines) > 0 {
		snap.Lines = req.Lines
	}
	if req.Taxable != "" {
		snap.Taxable = req.Taxable
	}
	if req.CGST != "" {
		snap.CGST = req.CGST
	}
	if req.SGST != "" {
		snap.SGST = req.SGST
	}
	if req.IGST != "" {
		snap.IGST = req.IGST
	}
	if req.InvoiceTotal != "" {
		snap.InvoiceTotal = req.InvoiceTotal
	}

	res, err := statutory.GenerateIRNWorkflow(ctx, s.store, s.prov, s.cored, companyID, voucherID, snap)
	if err != nil {
		if statutory.IsPermanent(err) {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
			return
		}
		// Transient: the workflow is resumable; surface 503 so the caller retries.
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, eInvoiceResponse{
		IRN:             res.IRN,
		AckNo:           res.AckNo,
		Status:          res.Status,
		PinnedVoucherNo: snap.VoucherNo,
	})
}

func (s *server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "statutoryd"})
}

func main() {
	coredBaseURL := env("CORED_BASE_URL", defaultCoredBaseURL)
	port := env("PORT", defaultPort)

	s := &server{
		store: statutory.NewMemStore(),
		prov:  statutory.FakeProvider{},
		cored: statutory.NewHTTPCoredClient(coredBaseURL),
	}

	r := chi.NewRouter()
	r.Get("/healthz", s.handleHealthz)
	r.Post("/companies/{companyID}/vouchers/{voucherID}/e-invoice", s.handleEInvoice)

	addr := ":" + port
	log.Printf("statutoryd serving on %s (cored=%s)", addr, coredBaseURL)
	srv := &http.Server{Addr: addr, Handler: r}
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("statutoryd: %v", err)
	}
}
