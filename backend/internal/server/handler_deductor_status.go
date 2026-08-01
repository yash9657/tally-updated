package server

// HTTP surface for the TDS deductor status — Item Zero step 2.
//
// This is the route whose ABSENCE was the blocking item after step 1. The post
// path refuses every TDS voucher for a company with no recorded status, and
// until this existed the only way to record one was raw SQL, so TDS did not work
// on any real deployment.
//
//	GET .../deductor-status            -> Display / deductor-status
//	PUT .../deductor-status/{taxYear}  -> Alter   / deductor-status
//
// THE READ AND THE WRITE SHARE A RESOURCE TOKEN, deliberately, and that is the
// opposite call from bank reconciliation next door. There the read (the whole
// bank position) and the write (ticking one cheque) are authorities over
// DIFFERENT objects, so one token would force them to be granted together. Here
// they are two authorities over the SAME small object — "what kind of entity is
// this company, and what did it turn over" — so one token keeps one rule
// governing both, exactly as the masters arms do.

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/compliance"
	"github.com/khata/cored/internal/masters"
)

// deductorStatusReq is the body of PUT .../deductor-status/{taxYear}.
//
// The tax year is in the PATH and not the body: it is the resource's identity
// (one row per company per year), and a body that could disagree with the URL is
// a question about which one wins.
type deductorStatusReq struct {
	// EntityCategory is Tally's "deductor type" — one of
	// compliance.KnownEntityCategories.
	EntityCategory string `json:"entity_category"`
	// PriorYearTurnoverRupees is a fixed-point decimal STRING (money is never a
	// float). EMPTY means "not applicable to this category", which is a
	// different statement from "0" and is stored as NULL.
	PriorYearTurnoverRupees string `json:"prior_year_turnover_rupees"`
	// PriorYearActivity is "BUSINESS" or "PROFESSION"; empty when the turnover
	// test does not apply.
	PriorYearActivity string `json:"prior_year_activity"`
	RecordedBy        string `json:"recorded_by"`
}

// deductorStatusView is the wire shape of one recorded year.
type deductorStatusView struct {
	TaxYearStart   int    `json:"tax_year_start"`
	TaxYearLabel   string `json:"tax_year_label"`
	EntityCategory string `json:"entity_category"`
	// GovernsPaymentsIn and its label are DERIVED and returned on purpose. The
	// row for 2025-26 decides payments made in 2026-27 (s.402(37)(b), "the tax
	// year immediately preceding"), and that off-by-one is the single most
	// confusable thing about this feature. Sending it means a screen states it
	// rather than an operator guessing which year to key.
	GovernsPaymentsIn      int    `json:"governs_payments_in"`
	GovernsPaymentsInLabel string `json:"governs_payments_in_label"`
	// Empty when the turnover test does not apply to the category. NOT "0":
	// see masters.DeductorStatusRecord.TurnoverKnown.
	PriorYearTurnoverRupees string `json:"prior_year_turnover_rupees"`
	PriorYearActivity       string `json:"prior_year_activity"`
	RecordedBy              string `json:"recorded_by"`
	RecordedAt              string `json:"recorded_at"`
}

// taxYearLabel renders 2025 as "2025-26" — the form every Indian statutory
// document uses, and the form that makes the preceding-year rule readable.
func taxYearLabel(start int) string {
	return strconv.Itoa(start) + "-" + strconv.Itoa((start+1)%100)
}

func toDeductorStatusView(r masters.DeductorStatusRecord) deductorStatusView {
	v := deductorStatusView{
		TaxYearStart:           r.TaxYearStart,
		TaxYearLabel:           taxYearLabel(r.TaxYearStart),
		EntityCategory:         r.EntityCategory,
		GovernsPaymentsIn:      r.GovernsPaymentsIn,
		GovernsPaymentsInLabel: taxYearLabel(r.GovernsPaymentsIn),
		PriorYearActivity:      r.PriorYearActivity,
		RecordedBy:             r.RecordedBy,
		RecordedAt:             r.RecordedAt,
	}
	if r.TurnoverKnown {
		v.PriorYearTurnoverRupees = r.PriorYearTurnoverRupees.StringFixed(4)
	}
	return v
}

// handleListDeductorStatus serves GET .../deductor-status.
//
// It also returns the CATEGORY VOCABULARY. A picker that hard-codes the fifteen
// categories in the client is a fourth copy of a closed list the migration
// already owns; serving it means a category added to compliance reaches the
// screen without a second edit, and a client can never offer one the database
// would refuse.
func (s *Server) handleListDeductorStatus(w http.ResponseWriter, r *http.Request) {
	rows, err := s.masters.ListDeductorStatuses(r.Context())
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	views := make([]deductorStatusView, 0, len(rows))
	for _, row := range rows {
		views = append(views, toDeductorStatusView(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"deductor_statuses":   views,
		"entity_categories":   compliance.KnownEntityCategories,
		"prior_year_activity": []string{compliance.ActivityBusiness, compliance.ActivityProfession},
	})
}

// handleSetDeductorStatus serves PUT .../deductor-status/{taxYear}.
func (s *Server) handleSetDeductorStatus(w http.ResponseWriter, r *http.Request) {
	taxYear, err := strconv.Atoi(chi.URLParam(r, "taxYear"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "tax year must be the calendar year the tax year begins in, e.g. 2025 for 2025-26")
		return
	}
	var req deductorStatusReq
	if !decodeJSON(w, r, &req) {
		return
	}

	rec := masters.DeductorStatusRecord{
		TaxYearStart:      taxYear,
		EntityCategory:    req.EntityCategory,
		PriorYearActivity: req.PriorYearActivity,
	}
	// An EMPTY turnover string means "the test does not apply", which is stored
	// as NULL. It is NOT parsed as zero: a zero would assert that this company
	// turned over nothing, which is a different and false claim, and it would
	// make an INDIVIDUAL below the limit look identical to a COMPANY.
	if req.PriorYearTurnoverRupees != "" {
		amount, err := decimal.NewFromString(req.PriorYearTurnoverRupees)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid prior_year_turnover_rupees")
			return
		}
		rec.TurnoverKnown = true
		rec.PriorYearTurnoverRupees = amount
	}

	if err := s.masters.SetDeductorStatus(r.Context(), rec, req.RecordedBy); err != nil {
		// A rejected status is the OPERATOR'S input being wrong, not the
		// server's — 400 with the sentence masters wrote, which names the
		// statute and says what to supply.
		if errors.Is(err, masters.ErrInvalidDeductorStatus) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tax_year_start":            taxYear,
		"tax_year_label":            taxYearLabel(taxYear),
		"governs_payments_in":       taxYear + 1,
		"governs_payments_in_label": taxYearLabel(taxYear + 1),
	})
}
