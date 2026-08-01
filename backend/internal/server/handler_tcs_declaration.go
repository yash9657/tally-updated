package server

// HTTP surface for s.394(2) manufacturing declarations — Item Zero step 3.
//
//	GET  .../tcs-declarations  -> Display / tcs-declaration
//	POST .../tcs-declarations  -> Create  / tcs-declaration
//
// POST rather than PUT-with-a-key, unlike deductor-status next door: the row's
// identity is the TRIPLE (buyer, section, tax year), and three path segments to
// address one small document reads worse than a body that carries them. It is
// still idempotent on that triple — re-filing corrects.
//
// WHY THIS SHIPS WITH THE POST-PATH WIRING AND NOT BEFORE IT. Until
// ledger/tcs.go read this table, a route here would have stored documents that
// changed nothing — the "correct code nothing calls" pattern that Item Zero
// exists to pay off. The two halves land together so a declaration filed here
// actually exempts a sale.

import (
	"errors"
	"net/http"

	"github.com/khata/cored/internal/masters"
)

// tcsDeclarationReq is the body of POST .../tcs-declarations.
//
// NOTE THE THREE FIELDS THAT ARE NOT HERE: for_manufacturing, not_for_trading
// and buyer_is_resident. s.394(2) requires all three, migration 0049 CHECKs all
// three, and a declaration that fails any of them cannot be stored at all — so
// offering them as flags would invite a request that looks reasonable and is
// unstorable. Filing a declaration IS the assertion that all three hold.
type tcsDeclarationReq struct {
	BuyerLedgerID string `json:"buyer_ledger_id"`
	// Section must be one s.394(2) reaches (Table Sl. Nos. 1-5). Motor vehicles
	// are Sl. No. 6 and are refused.
	Section      string `json:"section"`
	TaxYearStart int    `json:"tax_year_start"`
	// The date ON the document, and the date the collector RECEIVED it. Two
	// different facts; s.394(3)'s deadline runs off the second.
	DeclarationDate string `json:"declaration_date"`
	ReceivedDate    string `json:"received_date"`
	// BuyerPAN may be EMPTY, and that is a meaningful state rather than a
	// missing field: s.397(2)(f)(i) makes a PAN-less declaration invalid and (g)
	// then requires the PENAL rate — so the buyer pays 5% rather than 2%, which
	// is worse for them than furnishing nothing at all.
	BuyerPAN   string `json:"buyer_pan"`
	RecordedBy string `json:"recorded_by"`
}

type tcsDeclarationView struct {
	BuyerLedgerID   string `json:"buyer_ledger_id"`
	BuyerName       string `json:"buyer_name"`
	Section         string `json:"section"`
	TaxYearStart    int    `json:"tax_year_start"`
	TaxYearLabel    string `json:"tax_year_label"`
	DeclarationDate string `json:"declaration_date"`
	ReceivedDate    string `json:"received_date"`
	// ForwardDueDate is s.394(3) — the 7th of the month after receipt, by which
	// a copy must reach the Commissioner. DERIVED and sent so the screen can
	// show the obligation this document creates, rather than storing a document
	// that quietly builds a compliance trap.
	ForwardDueDate string `json:"forward_due_date"`
	BuyerPAN       string `json:"buyer_pan"`
	// PANValid false means the declaration is INVALID under s.397(2)(f)(i) and
	// the PENAL rate applies — it does NOT mean the exemption is merely absent.
	PANValid   bool   `json:"pan_valid"`
	RecordedBy string `json:"recorded_by"`
}

func (s *Server) handleListTCSDeclarations(w http.ResponseWriter, r *http.Request) {
	rows, err := s.masters.ListTCSDeclarations(r.Context())
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	views := make([]tcsDeclarationView, 0, len(rows))
	for _, d := range rows {
		views = append(views, tcsDeclarationView{
			BuyerLedgerID:   d.BuyerLedgerID.String(),
			BuyerName:       d.BuyerName,
			Section:         d.Section,
			TaxYearStart:    d.TaxYearStart,
			TaxYearLabel:    taxYearLabel(d.TaxYearStart),
			DeclarationDate: d.DeclarationDate,
			ReceivedDate:    d.ReceivedDate,
			ForwardDueDate:  d.ForwardDueDate,
			BuyerPAN:        d.BuyerPAN,
			PANValid:        d.PANValid,
			RecordedBy:      d.RecordedBy,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tcs_declarations": views,
		// The sections s.394(2) can reach, served so a picker cannot offer one
		// the statute does not.
		"eligible_sections": masters.TCSDeclarationSections(),
	})
}

func (s *Server) handleCreateTCSDeclaration(w http.ResponseWriter, r *http.Request) {
	var req tcsDeclarationReq
	if !decodeJSON(w, r, &req) {
		return
	}
	buyerID, err := optionalUUID(req.BuyerLedgerID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid buyer_ledger_id")
		return
	}
	err = s.masters.SetTCSDeclaration(r.Context(), masters.TCSDeclarationRecord{
		BuyerLedgerID:   buyerID,
		Section:         req.Section,
		TaxYearStart:    req.TaxYearStart,
		DeclarationDate: req.DeclarationDate,
		ReceivedDate:    req.ReceivedDate,
		BuyerPAN:        req.BuyerPAN,
	}, req.RecordedBy)
	if err != nil {
		if errors.Is(err, masters.ErrInvalidTCSDeclaration) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"buyer_ledger_id": buyerID.String(),
		"section":         req.Section,
		"tax_year_start":  req.TaxYearStart,
		"tax_year_label":  taxYearLabel(req.TaxYearStart),
	})
}
