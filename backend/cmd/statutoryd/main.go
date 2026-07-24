// Command statutoryd is the IRN e-invoice generation plane — a thin, separate
// deployable from cored. It runs the durable IRN workflow against a StepStore
// (Postgres when STATUTORYD_DSN is set, else in-memory), a deterministic
// FakeProvider, and an HTTPCoredClient pointed at a running cored, calling
// cored's freeze hook at IRN time.
//
// ENV:
//
//	CORED_BASE_URL      cored base URL (default http://localhost:8087)
//	CORED_SERVICE_TOKEN pre-minted service JWT (subject "svc:statutoryd") attached
//	                    as Authorization: Bearer on every cored call. Obtain it
//	                    from cored's dev POST /auth/token. With cored's auth on,
//	                    the freeze hook is rejected (401) without it.
//	PORT                HTTP listen port (default 8091)
//	STATUTORYD_DSN      Postgres DSN for the DURABLE StepStore (table
//	                    statutory_step, cored migration 0013). Use the app role,
//	                    e.g. postgres://tally_app:tally_app@localhost:5433/tally?sslmode=disable
//	                    UNSET => in-memory store: workflow state is lost on
//	                    restart (a crash between IRN generation and the freeze
//	                    hook would re-call the IRP and mint a second IRN).
//	                    statutoryd does NOT run migrations — cored owns all
//	                    schema; boot FAILS if statutory_step is missing or not
//	                    granted to the DSN's role.
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
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

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

// migrateHint is the operator guidance attached to every durable-store boot
// failure. statutoryd deliberately owns NO schema: cored's migration runner is
// the sole owner of all DDL, including statutory_step (migration 0013).
const migrateHint = "statutoryd does not run migrations (cored owns all schema) — apply them first: cd backend && go run ./cmd/cored migrate"

// newStepStore selects the workflow StepStore from the DSN.
//
//	dsn == ""  -> in-memory store + a LOUD warning (restarts lose progress).
//	dsn != ""  -> Postgres-backed statutory.PGStore, after two boot checks:
//	              (a) to_regclass proves the table exists, and
//	              (b) a SELECT proves this ROLE may actually read it.
//
// Check (b) is not redundant: to_regclass resolves a name and ignores
// privileges, so it returns non-NULL even when the GRANT to the connecting role
// was never made — which would sail past boot and then fail on the first
// Append, i.e. mid-workflow in production. Probing here converts that into a
// boot failure.
//
// Returns a close func for the pool (a no-op for the in-memory store) so the
// caller need not know which store it got.
func newStepStore(ctx context.Context, dsn string) (statutory.StepStore, func(), error) {
	if dsn == "" {
		log.Printf("statutoryd: WARNING STATUTORYD_DSN unset — workflow state is IN-MEMORY; restarts lose progress " +
			"(a crash between IRN generation and the freeze hook can mint a SECOND IRN). " +
			"Set STATUTORYD_DSN to the durable Postgres StepStore.")
		return statutory.NewMemStore(), func() {}, nil
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("statutoryd: STATUTORYD_DSN is not a usable Postgres DSN: %w", err)
	}

	var present bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.statutory_step') IS NOT NULL`).Scan(&present); err != nil {
		pool.Close()
		return nil, nil, fmt.Errorf("statutoryd: cannot query Postgres at STATUTORYD_DSN: %w; %s", err, migrateHint)
	}
	if !present {
		pool.Close()
		return nil, nil, fmt.Errorf("statutoryd: table statutory_step does not exist; %s", migrateHint)
	}
	if _, err := pool.Exec(ctx, `SELECT 1 FROM statutory_step LIMIT 1`); err != nil {
		pool.Close()
		return nil, nil, fmt.Errorf("statutoryd: statutory_step exists but is not readable by this role (missing GRANT?): %w; %s", err, migrateHint)
	}

	log.Printf("statutoryd: DURABLE StepStore on Postgres (table statutory_step) — workflow state survives restarts")
	return statutory.NewPGStore(pool), pool.Close, nil
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
	serviceToken := os.Getenv("CORED_SERVICE_TOKEN")
	port := env("PORT", defaultPort)

	if serviceToken == "" {
		log.Printf("statutoryd: WARNING CORED_SERVICE_TOKEN unset; cored freeze-hook calls will be rejected (401) when cored auth is on")
	}

	store, closeStore, err := newStepStore(context.Background(), os.Getenv("STATUTORYD_DSN"))
	if err != nil {
		log.Fatalf("%v", err)
	}
	defer closeStore()

	s := &server{
		store: store,
		prov:  statutory.FakeProvider{},
		cored: statutory.NewHTTPCoredClientWithToken(coredBaseURL, serviceToken),
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
