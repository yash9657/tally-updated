// Package server wires the cored HTTP surface: a chi router exposing the
// Phase 1 capabilities of the modular monolith — company and master creation,
// posting each of the six voucher classes, fetching each financial report,
// generating (and reconciling) GSTR-1, and triggering a Tally-XML migration
// import (which doubles as the Tally-XML ingress). It is deliberately thin:
// every request is decoded, scoped to a company via the db GUC guard, and
// delegated to the owning module. The modules hold the logic.
//
// The server is the composition root of the monolith (it is the only package
// permitted by archtest to import every sibling module). It opens ONE
// application-DSN pool (the least-privilege tally_app role) and shares it across
// all module services, so every write and read flows through the same
// tenant-scoped RunInTx GUC guard and the default-deny RLS policies.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/compliance"
	"github.com/khata/cored/internal/compliance/gstr"
	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/masters"
	"github.com/khata/cored/internal/migration"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/reporting"
)

// Request-body size caps guard against unbounded uploads. The Tally-XML import
// endpoint accepts a larger body than the JSON endpoints.
const (
	maxJSONBody = 1 << 20  // 1 MiB
	maxXMLBody  = 32 << 20 // 32 MiB
)

// Server holds the module services and the chi router. All services share one
// tenant-scoped pool.
type Server struct {
	pool     *db.Pool
	identity *identity.Service
	masters  *masters.Service
	ledger   *ledger.Service
	report   *reporting.Service
	gstr     *gstr.Service
	importer *migration.Importer
	router   chi.Router
}

// New constructs a Server backed by the application-DSN connection pool and
// wires every Phase 1 module service over it.
func New(ctx context.Context, appDSN string) (*Server, error) {
	pool, err := db.New(ctx, appDSN)
	if err != nil {
		return nil, err
	}
	m := masters.NewService(pool)
	l := ledger.NewService(pool)
	s := &Server{
		pool:     pool,
		identity: identity.NewService(pool),
		masters:  m,
		ledger:   l,
		report:   reporting.NewService(pool),
		gstr:     gstr.NewService(pool),
		importer: migration.NewImporter(m, l),
	}
	s.routes()
	return s, nil
}

// Close releases resources.
func (s *Server) Close() { s.pool.Close() }

// Router exposes the configured chi router so tests can drive the handlers via
// httptest without binding a socket.
func (s *Server) Router() http.Handler { return s.router }

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

	// Company lifecycle (identity).
	r.Post("/v1/companies", s.handleCreateCompany)

	r.Route("/v1/companies/{companyID}", func(r chi.Router) {
		// Masters.
		r.Post("/groups", s.handleCreateGroup)
		r.Post("/ledgers", s.handleCreateLedger)
		r.Post("/voucher-types", s.handleCreateVoucherType)
		r.Post("/units", s.handleCreateUnit)
		r.Post("/stock-items", s.handleCreateStockItem)

		// Vouchers: one endpoint per accounting class (F4-F9). The body is the
		// same generic voucher shape; the path fixes the class.
		r.Post("/vouchers/sales", s.postVoucher(ledger.Sales))
		r.Post("/vouchers/purchase", s.postVoucher(ledger.Purchase))
		r.Post("/vouchers/receipt", s.postVoucher(ledger.Receipt))
		r.Post("/vouchers/payment", s.postVoucher(ledger.Payment))
		r.Post("/vouchers/contra", s.postVoucher(ledger.Contra))
		r.Post("/vouchers/journal", s.postVoucher(ledger.Journal))

		// Alter: PUT a full restatement of an existing voucher. One Put per literal
		// type segment keeps the class compile-time fixed, exactly like the Posts.
		// {voucherID} carries the existing logical voucher id (never reallocated).
		r.Put("/vouchers/sales/{voucherID}", s.alterVoucher(ledger.Sales))
		r.Put("/vouchers/purchase/{voucherID}", s.alterVoucher(ledger.Purchase))
		r.Put("/vouchers/receipt/{voucherID}", s.alterVoucher(ledger.Receipt))
		r.Put("/vouchers/payment/{voucherID}", s.alterVoucher(ledger.Payment))
		r.Put("/vouchers/contra/{voucherID}", s.alterVoucher(ledger.Contra))
		r.Put("/vouchers/journal/{voucherID}", s.alterVoucher(ledger.Journal))

		// Delete: append a tombstone. Class-agnostic — a delete needs only the id.
		r.Delete("/vouchers/{voucherID}", s.handleDeleteVoucher)

		// Backward-compatible alias for the original F8 Sales endpoint.
		r.Post("/sales", s.postVoucher(ledger.Sales))

		// Reports.
		r.Get("/trial-balance", s.handleTrialBalance)
		r.Get("/balance-sheet", s.handleBalanceSheet)
		r.Get("/profit-and-loss", s.handleProfitAndLoss)
		r.Get("/day-book", s.handleDayBook)
		r.Get("/outstandings", s.handleOutstandings)

		// GST.
		r.Get("/gstr1", s.handleGSTR1)

		// Migration import (Tally-XML ingress).
		r.Post("/import/tally-xml", s.handleImportTallyXML)
	})

	s.router = r
}

// --------------------------------------------------------------------------
// Health
// --------------------------------------------------------------------------

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if err := s.pool.Ping(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "db_unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --------------------------------------------------------------------------
// Company
// --------------------------------------------------------------------------

type companyReq struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	FYStartDate  string `json:"fy_start_date"`
	BaseCurrency string `json:"base_currency"`
}

func (s *Server) handleCreateCompany(w http.ResponseWriter, r *http.Request) {
	var req companyReq
	if !decodeJSON(w, r, &req) {
		return
	}
	id := uuid.New()
	if req.ID != "" {
		parsed, err := uuid.Parse(req.ID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid company id")
			return
		}
		id = parsed
	}
	// CreateCompany requires the context to be scoped to the same company id
	// (the insert is itself subject to RLS).
	ctx := db.WithCompany(r.Context(), id)
	created, err := s.identity.CreateCompany(ctx, identity.Company{
		ID:           id,
		Name:         req.Name,
		FYStartDate:  req.FYStartDate,
		BaseCurrency: req.BaseCurrency,
	})
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"company_id": created.String()})
}

// --------------------------------------------------------------------------
// Masters
// --------------------------------------------------------------------------

type groupReq struct {
	Name               string `json:"name"`
	ParentName         string `json:"parent_name"`
	Nature             string `json:"nature"`
	IsPL               bool   `json:"is_pl"`
	BehavesAsSubledger bool   `json:"behaves_as_subledger"`
	CreatedBy          string `json:"created_by"`
}

func (s *Server) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	var req groupReq
	if !decodeJSON(w, r, &req) {
		return
	}
	id, err := s.masters.CreateGroup(ctx, masters.Group{
		Name:               req.Name,
		ParentName:         req.ParentName,
		Nature:             masters.Nature(req.Nature),
		IsPL:               req.IsPL,
		BehavesAsSubledger: req.BehavesAsSubledger,
	}, req.CreatedBy)
	if err != nil {
		writeMastersError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"group_id": id.String()})
}

type ledgerReq struct {
	Name               string `json:"name"`
	GroupName          string `json:"group_name"`
	OpeningBalance     string `json:"opening_balance"`
	OpeningDC          string `json:"opening_dc"`
	MaintainBillByBill bool   `json:"maintain_bill_by_bill"`
	GSTIN              string `json:"gstin"`
	StateCode          string `json:"state_code"`
	RegistrationType   string `json:"registration_type"`
	PAN                string `json:"pan"`
	CreatedBy          string `json:"created_by"`
}

func (s *Server) handleCreateLedger(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	var req ledgerReq
	if !decodeJSON(w, r, &req) {
		return
	}
	opening, err := optionalDecimal(req.OpeningBalance)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid opening_balance")
		return
	}
	id, err := s.masters.CreateLedger(ctx, masters.Ledger{
		Name:               req.Name,
		GroupName:          req.GroupName,
		OpeningBalance:     opening,
		OpeningDC:          req.OpeningDC,
		MaintainBillByBill: req.MaintainBillByBill,
		GSTIN:              req.GSTIN,
		StateCode:          req.StateCode,
		RegistrationType:   masters.GSTRegistrationType(req.RegistrationType),
		PAN:                req.PAN,
	}, req.CreatedBy)
	if err != nil {
		writeMastersError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"ledger_id": id.String()})
}

type voucherTypeReq struct {
	Name            string `json:"name"`
	ParentClass     string `json:"parent_class"`
	NumberingMethod string `json:"numbering_method"`
	AffectsStock    bool   `json:"affects_stock"`
	IsAccounting    bool   `json:"is_accounting"`
	CreatedBy       string `json:"created_by"`
}

func (s *Server) handleCreateVoucherType(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	var req voucherTypeReq
	if !decodeJSON(w, r, &req) {
		return
	}
	id, err := s.masters.CreateVoucherType(ctx, masters.VoucherType{
		Name:            req.Name,
		ParentClass:     req.ParentClass,
		NumberingMethod: req.NumberingMethod,
		AffectsStock:    req.AffectsStock,
		IsAccounting:    req.IsAccounting,
	}, req.CreatedBy)
	if err != nil {
		writeMastersError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"voucher_type_id": id.String()})
}

type unitReq struct {
	Symbol        string `json:"symbol"`
	FormalName    string `json:"formal_name"`
	DecimalPlaces int    `json:"decimal_places"`
	CreatedBy     string `json:"created_by"`
}

func (s *Server) handleCreateUnit(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	var req unitReq
	if !decodeJSON(w, r, &req) {
		return
	}
	id, err := s.masters.CreateUnit(ctx, masters.Unit{
		Symbol:        req.Symbol,
		FormalName:    req.FormalName,
		DecimalPlaces: req.DecimalPlaces,
	}, req.CreatedBy)
	if err != nil {
		writeMastersError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"unit_id": id.String()})
}

type stockItemReq struct {
	Name        string `json:"name"`
	UnitSymbol  string `json:"unit_symbol"`
	HSN         string `json:"hsn"`
	GSTRatePct  string `json:"gst_rate_pct"`
	OpeningQty  string `json:"opening_qty"`
	OpeningRate string `json:"opening_rate"`
	CreatedBy   string `json:"created_by"`
}

func (s *Server) handleCreateStockItem(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	var req stockItemReq
	if !decodeJSON(w, r, &req) {
		return
	}
	rate, err := optionalDecimal(req.GSTRatePct)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid gst_rate_pct")
		return
	}
	qty, err := optionalDecimal(req.OpeningQty)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid opening_qty")
		return
	}
	openRate, err := optionalDecimal(req.OpeningRate)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid opening_rate")
		return
	}
	id, err := s.masters.CreateStockItem(ctx, masters.StockItem{
		Name:        req.Name,
		UnitSymbol:  req.UnitSymbol,
		HSN:         req.HSN,
		GSTRatePct:  rate,
		OpeningQty:  qty,
		OpeningRate: openRate,
	}, req.CreatedBy)
	if err != nil {
		writeMastersError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"stock_item_id": id.String()})
}

// --------------------------------------------------------------------------
// Vouchers
// --------------------------------------------------------------------------

type postingReq struct {
	LedgerID string `json:"ledger_id"`
	DrCr     string `json:"dr_cr"`
	Amount   string `json:"amount"`
}

type billAllocationReq struct {
	LedgerID string `json:"ledger_id"`
	RefName  string `json:"ref_name"`
	RefType  string `json:"ref_type"`
	DrCr     string `json:"dr_cr"`
	Amount   string `json:"amount"`
	DueDate  string `json:"due_date"`
}

type gstLineReq struct {
	TaxableValue string `json:"taxable_value"`
	GSTRatePct   string `json:"gst_rate_pct"`
	HSN          string `json:"hsn"`
}

type gstReq struct {
	RulesetVersion         string       `json:"ruleset_version"`
	SupplierStateCode      string       `json:"supplier_state_code"`
	PlaceOfSupplyStateCode string       `json:"place_of_supply_state_code"`
	ReverseCharge          bool         `json:"reverse_charge"`
	Exempt                 bool         `json:"exempt"`
	Lines                  []gstLineReq `json:"lines"`
	CGSTLedgerID           string       `json:"cgst_ledger_id"`
	SGSTLedgerID           string       `json:"sgst_ledger_id"`
	IGSTLedgerID           string       `json:"igst_ledger_id"`
}

type voucherReq struct {
	VoucherTypeID   string              `json:"voucher_type_id"`
	PartyLedgerID   string              `json:"party_ledger_id"`
	VoucherNo       string              `json:"voucher_no"`
	VoucherDate     string              `json:"voucher_date"`
	Narration       string              `json:"narration"`
	Party           string              `json:"party"`
	CreatedBy       string              `json:"created_by"`
	Postings        []postingReq        `json:"postings"`
	BillAllocations []billAllocationReq `json:"bill_allocations"`
	GST             *gstReq             `json:"gst"`
}

// postVoucher returns a handler bound to a fixed accounting class. The same
// generic body posts any of the six classes; the path determines the class.
func (s *Server) postVoucher(class ledger.Class) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, ok := s.scopeCtx(w, r)
		if !ok {
			return
		}
		var req voucherReq
		if !decodeJSON(w, r, &req) {
			return
		}
		in, ok := buildVoucherInput(w, class, req)
		if !ok {
			return
		}
		res, err := s.ledger.PostVoucher(ctx, in)
		if err != nil {
			if errors.Is(err, db.ErrNoCompany) {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, voucherResultJSON(res))
	}
}

// alterVoucher returns a handler bound to a fixed accounting class that appends
// an ALTER version to an existing logical voucher. It mirrors postVoucher: the
// class is fixed by the path, the {voucherID} segment carries the existing
// logical voucher id, and the body is a FULL restatement (all postings resent).
// The supplied voucher_no/voucher_date are stamped verbatim, so a client that
// resends the loaded number/date preserves them (Tally never auto-renumbers on
// alter).
func (s *Server) alterVoucher(class ledger.Class) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, ok := s.scopeCtx(w, r)
		if !ok {
			return
		}
		voucherID, err := uuid.Parse(chi.URLParam(r, "voucherID"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid voucher id")
			return
		}
		var req voucherReq
		if !decodeJSON(w, r, &req) {
			return
		}
		in, ok := buildVoucherInput(w, class, req)
		if !ok {
			return
		}
		res, err := s.ledger.AlterVoucher(ctx, voucherID, in)
		if err != nil {
			writeVoucherEditError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, voucherResultJSON(res))
	}
}

// handleDeleteVoucher appends a DELETE tombstone to an existing logical voucher.
// A delete needs only the logical id (the class is already recorded on the prior
// versions), so the route is class-agnostic. The optional ?created_by= query
// param stamps the deleting user onto the tombstone, consistent with the
// Tally-XML import endpoint. The tombstone reverses the voucher out of every
// balance and report while its versions remain in the audit log.
func (s *Server) handleDeleteVoucher(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	voucherID, err := uuid.Parse(chi.URLParam(r, "voucherID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid voucher id")
		return
	}
	createdBy := r.URL.Query().Get("created_by")
	res, err := s.ledger.DeleteVoucher(ctx, voucherID, createdBy)
	if err != nil {
		writeVoucherEditError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"voucher_id":         res.VoucherID.String(),
		"voucher_version_id": res.VoucherVersionID.String(),
		"version_no":         res.VersionNo,
		"op":                 string(ledger.OpDelete),
	})
}

// voucherResultJSON builds the response body shared by the POST and PUT voucher
// handlers so the shape never drifts between create and alter.
func voucherResultJSON(res ledger.PostResult) map[string]any {
	return map[string]any{
		"voucher_id":         res.VoucherID.String(),
		"voucher_version_id": res.VoucherVersionID.String(),
		"version_no":         res.VersionNo,
		"cgst":               res.Tax.CGST.StringFixed(4),
		"sgst":               res.Tax.SGST.StringFixed(4),
		"igst":               res.Tax.IGST.StringFixed(4),
	}
}

// writeVoucherEditError maps an alter/delete error to an HTTP status: an unknown
// voucher is a 404, a missing company scope is a 400, and anything else
// (unbalanced postings, GST mismatch, ledger validation) is a 422 — the same
// mapping the POST path uses.
func writeVoucherEditError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ledger.ErrVoucherNotFound):
		writeError(w, http.StatusNotFound, "voucher not found")
	case errors.Is(err, db.ErrNoCompany):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	}
}

// buildVoucherInput converts a decoded voucherReq into a ledger.VoucherInput,
// writing a 400 and returning ok=false on any malformed field.
func buildVoucherInput(w http.ResponseWriter, class ledger.Class, req voucherReq) (ledger.VoucherInput, bool) {
	in := ledger.VoucherInput{
		Class:       class,
		VoucherNo:   req.VoucherNo,
		VoucherDate: req.VoucherDate,
		Narration:   req.Narration,
		Party:       req.Party,
		CreatedBy:   req.CreatedBy,
	}
	var err error
	if in.VoucherTypeID, err = optionalUUID(req.VoucherTypeID); err != nil {
		writeError(w, http.StatusBadRequest, "invalid voucher_type_id")
		return in, false
	}
	if in.PartyLedgerID, err = optionalUUID(req.PartyLedgerID); err != nil {
		writeError(w, http.StatusBadRequest, "invalid party_ledger_id")
		return in, false
	}
	for _, p := range req.Postings {
		lid, err := uuid.Parse(p.LedgerID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid posting ledger_id")
			return in, false
		}
		amt, err := decimal.NewFromString(p.Amount)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid posting amount")
			return in, false
		}
		in.Postings = append(in.Postings, ledger.PostingInput{
			LedgerID: lid,
			DrCr:     ledger.DrCr(p.DrCr),
			Amount:   amt,
		})
	}
	for _, b := range req.BillAllocations {
		lid, err := uuid.Parse(b.LedgerID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid bill allocation ledger_id")
			return in, false
		}
		amt, err := decimal.NewFromString(b.Amount)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid bill allocation amount")
			return in, false
		}
		in.BillAllocations = append(in.BillAllocations, ledger.BillAllocationInput{
			LedgerID: lid,
			RefName:  b.RefName,
			RefType:  ledger.RefType(b.RefType),
			DrCr:     ledger.DrCr(b.DrCr),
			Amount:   amt,
			DueDate:  b.DueDate,
		})
	}
	if req.GST != nil {
		gst, ok := buildGST(w, *req.GST)
		if !ok {
			return in, false
		}
		in.GST = gst
	}
	return in, true
}

// buildGST converts the GST request block into ledger.GSTParams, writing a 400
// and returning ok=false on any malformed field.
func buildGST(w http.ResponseWriter, g gstReq) (*ledger.GSTParams, bool) {
	out := &ledger.GSTParams{
		RulesetVersion:         g.RulesetVersion,
		SupplierStateCode:      g.SupplierStateCode,
		PlaceOfSupplyStateCode: g.PlaceOfSupplyStateCode,
		ReverseCharge:          g.ReverseCharge,
		Exempt:                 g.Exempt,
	}
	for _, ln := range g.Lines {
		tv, err := decimal.NewFromString(ln.TaxableValue)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid gst line taxable_value")
			return nil, false
		}
		rate, err := decimal.NewFromString(ln.GSTRatePct)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid gst line gst_rate_pct")
			return nil, false
		}
		out.Lines = append(out.Lines, compliance.Line{
			TaxableValue: tv,
			GSTRatePct:   rate,
			HSN:          ln.HSN,
		})
	}
	var err error
	if out.CGSTLedgerID, err = optionalUUID(g.CGSTLedgerID); err != nil {
		writeError(w, http.StatusBadRequest, "invalid cgst_ledger_id")
		return nil, false
	}
	if out.SGSTLedgerID, err = optionalUUID(g.SGSTLedgerID); err != nil {
		writeError(w, http.StatusBadRequest, "invalid sgst_ledger_id")
		return nil, false
	}
	if out.IGSTLedgerID, err = optionalUUID(g.IGSTLedgerID); err != nil {
		writeError(w, http.StatusBadRequest, "invalid igst_ledger_id")
		return nil, false
	}
	return out, true
}

// --------------------------------------------------------------------------
// Reports
// --------------------------------------------------------------------------

func (s *Server) handleTrialBalance(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
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

func (s *Server) handleBalanceSheet(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
	bs, err := s.report.BalanceSheet(ctx, companyID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"assets":      sectionJSON(bs.Assets),
		"liabilities": sectionJSON(bs.Liabilities),
		"net_profit":  bs.NetProfit.StringFixed(4),
		"balanced":    bs.IsBalanced(),
	})
}

func (s *Server) handleProfitAndLoss(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
	pl, err := s.report.ProfitAndLoss(ctx, companyID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"income":     sectionJSON(pl.Income),
		"expenses":   sectionJSON(pl.Expenses),
		"net_profit": pl.NetProfit.StringFixed(4),
	})
}

func (s *Server) handleDayBook(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")
	dbk, err := s.report.DayBook(ctx, companyID, from, to)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	entries := make([]map[string]any, 0, len(dbk.Entries))
	for _, e := range dbk.Entries {
		entries = append(entries, voucherEntryJSON(e))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"from":    dbk.From,
		"to":      dbk.To,
		"entries": entries,
	})
}

func (s *Server) handleOutstandings(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
	var kind reporting.OutstandingKind
	switch r.URL.Query().Get("kind") {
	case "", "RECEIVABLE", "receivable":
		kind = reporting.Receivables
	case "PAYABLE", "payable":
		kind = reporting.Payables
	default:
		writeError(w, http.StatusBadRequest, "invalid kind (RECEIVABLE|PAYABLE)")
		return
	}
	asOn := r.URL.Query().Get("as_on")
	out, err := s.report.Outstandings(ctx, companyID, kind, asOn, nil)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	parties := make([]map[string]any, 0, len(out.Parties))
	for _, p := range out.Parties {
		bills := make([]map[string]any, 0, len(p.Bills))
		for _, b := range p.Bills {
			bills = append(bills, map[string]any{
				"ref_name":     b.RefName,
				"pending":      b.Pending.StringFixed(4),
				"due_date":     b.DueDate,
				"overdue_days": b.OverdueDays,
				"slab":         b.SlabLabel,
			})
		}
		parties = append(parties, map[string]any{
			"ledger":  p.LedgerName,
			"group":   p.GroupName,
			"pending": p.Pending.StringFixed(4),
			"bills":   bills,
		})
	}
	slabs := make([]map[string]string, 0, len(out.SlabTotals))
	for _, st := range out.SlabTotals {
		slabs = append(slabs, map[string]string{
			"label":  st.Label,
			"amount": st.Amount.StringFixed(4),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"kind":        string(out.Kind),
		"as_on":       out.AsOn,
		"parties":     parties,
		"slab_totals": slabs,
		"total":       out.Total.StringFixed(4),
	})
}

// --------------------------------------------------------------------------
// GST
// --------------------------------------------------------------------------

func (s *Server) handleGSTR1(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")
	if from == "" || to == "" {
		writeError(w, http.StatusBadRequest, "from and to query params are required (YYYY-MM-DD)")
		return
	}
	opts := gstr.Options{GSTIN: r.URL.Query().Get("gstin")}
	var err error
	if opts.CGSTLedgerID, err = optionalUUID(r.URL.Query().Get("cgst_ledger_id")); err != nil {
		writeError(w, http.StatusBadRequest, "invalid cgst_ledger_id")
		return
	}
	if opts.SGSTLedgerID, err = optionalUUID(r.URL.Query().Get("sgst_ledger_id")); err != nil {
		writeError(w, http.StatusBadRequest, "invalid sgst_ledger_id")
		return
	}
	if opts.IGSTLedgerID, err = optionalUUID(r.URL.Query().Get("igst_ledger_id")); err != nil {
		writeError(w, http.StatusBadRequest, "invalid igst_ledger_id")
		return
	}

	// When the caller supplied any output-tax ledger, reconcile the return
	// against the books and report whether it ties out to the paisa.
	if opts.CGSTLedgerID != uuid.Nil || opts.SGSTLedgerID != uuid.Nil || opts.IGSTLedgerID != uuid.Nil {
		g, rec, err := s.gstr.Reconcile(ctx, from, to, opts)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"gstr1":             g,
			"reconciliation_ok": rec.Balanced(),
		})
		return
	}

	g, err := s.gstr.Generate(ctx, from, to, opts)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, g)
}

// --------------------------------------------------------------------------
// Migration import (Tally-XML ingress)
// --------------------------------------------------------------------------

func (s *Server) handleImportTallyXML(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	createdBy := r.URL.Query().Get("created_by")
	r.Body = http.MaxBytesReader(w, r.Body, maxXMLBody)
	// The importer parses the Tally ENVELOPE and posts each voucher through the
	// foundation ledger path (the SOLE writer), inheriting every invariant.
	res, err := s.importer.ImportReader(ctx, r.Body, createdBy)
	if err != nil {
		if errors.Is(err, db.ErrNoCompany) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	posted := make([]map[string]any, 0, len(res.Posted))
	for _, p := range res.Posted {
		posted = append(posted, map[string]any{
			"voucher_id":         p.VoucherID.String(),
			"voucher_version_id": p.VoucherVersionID.String(),
			"version_no":         p.VersionNo,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"source_company":  res.SourceCompany,
		"vouchers_posted": res.VouchersPosted,
		"ledgers_ensured": res.LedgersEnsured,
		"posted":          posted,
	})
}

// --------------------------------------------------------------------------
// Shared helpers
// --------------------------------------------------------------------------

// scopeCtxID parses the {companyID} path param, binds it into the request
// context for the GUC guard, and returns both. On a bad id it writes a 400 and
// returns ok=false.
func (s *Server) scopeCtxID(w http.ResponseWriter, r *http.Request) (context.Context, uuid.UUID, bool) {
	companyID, err := uuid.Parse(chi.URLParam(r, "companyID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid company id")
		return nil, uuid.Nil, false
	}
	return db.WithCompany(r.Context(), companyID), companyID, true
}

// scopeCtx is scopeCtxID without surfacing the id (for handlers that only need
// the scoped context).
func (s *Server) scopeCtx(w http.ResponseWriter, r *http.Request) (context.Context, bool) {
	ctx, _, ok := s.scopeCtxID(w, r)
	return ctx, ok
}

// sectionJSON serialises a report Section (and its node tree) to a plain map.
func sectionJSON(sec reporting.Section) map[string]any {
	return map[string]any{
		"title": sec.Title,
		"nodes": nodesJSON(sec.Nodes),
		"total": sec.Total.StringFixed(4),
	}
}

func nodesJSON(nodes []reporting.StatementNode) []map[string]any {
	out := make([]map[string]any, 0, len(nodes))
	for _, n := range nodes {
		m := map[string]any{
			"kind":   string(n.Kind),
			"name":   n.Name,
			"amount": n.Amount.StringFixed(4),
		}
		if n.ID != uuid.Nil {
			m["id"] = n.ID.String()
		}
		if len(n.Children) > 0 {
			m["children"] = nodesJSON(n.Children)
		}
		out = append(out, m)
	}
	return out
}

func voucherEntryJSON(e reporting.VoucherEntry) map[string]any {
	postings := make([]map[string]string, 0, len(e.Postings))
	for _, p := range e.Postings {
		postings = append(postings, map[string]string{
			"ledger": p.LedgerName,
			"dr_cr":  p.DrCr,
			"amount": p.Amount.StringFixed(4),
		})
	}
	return map[string]any{
		"voucher_id":   e.VoucherID.String(),
		"version_no":   e.VersionNo,
		"op":           e.Op,
		"class":        e.Class,
		"voucher_type": e.VoucherTypeName,
		"voucher_no":   e.VoucherNo,
		"date":         e.Date,
		"narration":    e.Narration,
		"party":        e.Party,
		"debit":        e.Debit.StringFixed(4),
		"credit":       e.Credit.StringFixed(4),
		"postings":     postings,
	}
}

// decodeJSON decodes a JSON request body into v, capping the body size and
// writing a 400 on failure. It returns false when the caller should stop.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "empty request body")
		} else {
			writeError(w, http.StatusBadRequest, "invalid json body")
		}
		return false
	}
	return true
}

// optionalDecimal parses a possibly-empty decimal string, returning zero for an
// empty string.
func optionalDecimal(s string) (decimal.Decimal, error) {
	if s == "" {
		return decimal.Zero, nil
	}
	return decimal.NewFromString(s)
}

// optionalUUID parses a possibly-empty UUID string, returning uuid.Nil for an
// empty string.
func optionalUUID(s string) (uuid.UUID, error) {
	if s == "" {
		return uuid.Nil, nil
	}
	return uuid.Parse(s)
}

// writeMastersError maps a masters service error to an HTTP status: not-found
// and no-company are client errors, everything else is unprocessable.
func writeMastersError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrNoCompany):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, masters.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	default:
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
