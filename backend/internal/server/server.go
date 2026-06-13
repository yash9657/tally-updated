// Package server wires the cored HTTP surface: a chi router with a health
// check and a minimal endpoint that posts an F8 Sales voucher through the
// ledger module. It is deliberately thin — the modules hold the logic.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/reporting"
)

// Server holds the dependencies and the chi router.
type Server struct {
	pool   *db.Pool
	ledger *ledger.Service
	report *reporting.Service
	router chi.Router
}

// New constructs a Server backed by the application-DSN connection pool.
func New(ctx context.Context, appDSN string) (*Server, error) {
	pool, err := db.New(ctx, appDSN)
	if err != nil {
		return nil, err
	}
	s := &Server{
		pool:   pool,
		ledger: ledger.NewService(pool),
		report: reporting.NewService(pool),
	}
	s.routes()
	return s, nil
}

// Close releases resources.
func (s *Server) Close() { s.pool.Close() }

// ListenAndServe starts the HTTP server on addr.
func (s *Server) ListenAndServe(addr string) error {
	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           s.router,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return httpSrv.ListenAndServe()
}

func (s *Server) routes() {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Get("/healthz", s.handleHealthz)
	r.Post("/v1/companies/{companyID}/sales", s.handlePostSale)
	r.Get("/v1/companies/{companyID}/trial-balance", s.handleTrialBalance)
	s.router = r
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if err := s.pool.Ping(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "db_unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// salePostingReq is one posting line in a sale request.
type salePostingReq struct {
	LedgerID string `json:"ledger_id"`
	DrCr     string `json:"dr_cr"`
	Amount   string `json:"amount"`
}

// saleReq is the minimal F8 Sales post request body.
type saleReq struct {
	VoucherTypeID string           `json:"voucher_type_id"`
	VoucherNo     string           `json:"voucher_no"`
	VoucherDate   string           `json:"voucher_date"`
	Narration     string           `json:"narration"`
	Party         string           `json:"party"`
	CreatedBy     string           `json:"created_by"`
	Postings      []salePostingReq `json:"postings"`
}

func (s *Server) handlePostSale(w http.ResponseWriter, r *http.Request) {
	companyID, err := uuid.Parse(chi.URLParam(r, "companyID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid company id")
		return
	}

	var req saleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}

	vtID, err := uuid.Parse(req.VoucherTypeID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid voucher_type_id")
		return
	}

	in := ledger.SalesVoucherInput{
		VoucherTypeID: vtID,
		VoucherNo:     req.VoucherNo,
		VoucherDate:   req.VoucherDate,
		Narration:     req.Narration,
		Party:         req.Party,
		CreatedBy:     req.CreatedBy,
	}
	for _, p := range req.Postings {
		lid, err := uuid.Parse(p.LedgerID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid posting ledger_id")
			return
		}
		amt, err := decimal.NewFromString(p.Amount)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid posting amount")
			return
		}
		in.Postings = append(in.Postings, ledger.PostingInput{
			LedgerID: lid,
			DrCr:     ledger.DrCr(p.DrCr),
			Amount:   amt,
		})
	}

	ctx := db.WithCompany(r.Context(), companyID)
	res, err := s.ledger.PostSalesVoucher(ctx, in)
	if err != nil {
		if errors.Is(err, db.ErrNoCompany) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"voucher_id":         res.VoucherID.String(),
		"voucher_version_id": res.VoucherVersionID.String(),
		"version_no":         res.VersionNo,
	})
}

func (s *Server) handleTrialBalance(w http.ResponseWriter, r *http.Request) {
	companyID, err := uuid.Parse(chi.URLParam(r, "companyID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid company id")
		return
	}
	ctx := db.WithCompany(r.Context(), companyID)
	tb, err := s.report.TrialBalance(ctx, companyID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	rows := make([]map[string]string, 0, len(tb.Rows))
	for _, row := range tb.Rows {
		rows = append(rows, map[string]string{
			"ledger": row.LedgerName,
			"group":  row.GroupName,
			"debit":  row.Debit.StringFixed(4),
			"credit": row.Credit.StringFixed(4),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"rows":         rows,
		"total_debit":  tb.TotalDebit.StringFixed(4),
		"total_credit": tb.TotalCredit.StringFixed(4),
		"balanced":     tb.IsBalanced(),
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
