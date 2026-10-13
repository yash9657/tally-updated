package server

// handler_currency.go — T8 multi-currency: the currency master and its rates.
//
// ⛔ THE RATE IS A STRING ON THE WIRE, BOTH WAYS. It is NUMERIC(19,10) in the
// database and decimal.Decimal in Go, and it must never become a JSON number:
// a rate is the one value in this product that carries more than four decimals,
// and JSON numbers are float64 in every client that parses them. See
// docs/design/t8-multi-currency-model.md Q1.

import (
	"net/http"
	"strings"

	"github.com/khata/cored/internal/masters"
)

type currencyReq struct {
	Code       string `json:"code"`
	Symbol     string `json:"symbol"`
	FormalName string `json:"formal_name"`
	// DecimalPlaces is a POINTER so an explicit 0 is distinguishable from an
	// absent field — a zero-decimal currency (JPY, KRW) is real, and defaulting
	// it to 2 because the field decoded as the zero value would render every
	// yen amount with two decimals it does not have.
	DecimalPlaces *int   `json:"decimal_places"`
	CreatedBy     string `json:"created_by"`
}

type currencyRateReq struct {
	Code          string `json:"code"`
	EffectiveDate string `json:"effective_date"`
	// Rate is a STRING. See the file header.
	Rate      string `json:"rate"`
	CreatedBy string `json:"created_by"`
}

type currencyListItem struct {
	ID            string `json:"id"`
	Code          string `json:"code"`
	Symbol        string `json:"symbol"`
	FormalName    string `json:"formal_name"`
	DecimalPlaces int    `json:"decimal_places"`
	// LatestRate is "" when no rate has ever been entered for this currency.
	//
	// ⚠️ EMPTY IS A STATE THE CLIENT MUST RENDER, NOT A ZERO TO SUBSTITUTE. A
	// currency with no rate cannot be used to key a voucher; showing "0.0000"
	// would offer a choice that silently values the whole invoice at nothing.
	LatestRate     string `json:"latest_rate"`
	LatestRateDate string `json:"latest_rate_date"`
}

func (s *Server) handleCreateCurrency(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	var req currencyReq
	if !decodeJSON(w, r, &req) {
		return
	}
	// Tally's own default for a currency it does not know: two decimals.
	decimals := 2
	if req.DecimalPlaces != nil {
		decimals = *req.DecimalPlaces
	}
	id, err := s.masters.CreateCurrency(ctx, masters.Currency{
		Code:          strings.TrimSpace(req.Code),
		Symbol:        strings.TrimSpace(req.Symbol),
		FormalName:    strings.TrimSpace(req.FormalName),
		DecimalPlaces: decimals,
	}, req.CreatedBy)
	if err != nil {
		writeMastersError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"currency_id": id.String()})
}

func (s *Server) handleListCurrencies(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	list, err := s.masters.ListCurrencies(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list currencies: "+err.Error())
		return
	}
	out := make([]currencyListItem, 0, len(list))
	for _, c := range list {
		out = append(out, currencyListItem{
			ID: c.ID.String(), Code: c.Code, Symbol: c.Symbol,
			FormalName: c.FormalName, DecimalPlaces: c.DecimalPlaces,
			LatestRate: c.LatestRate, LatestRateDate: c.LatestRateDate,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"currencies": out})
}

func (s *Server) handleSetCurrencyRate(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	var req currencyRateReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.masters.SetExchangeRate(ctx,
		strings.TrimSpace(req.Code),
		strings.TrimSpace(req.EffectiveDate),
		strings.TrimSpace(req.Rate),
		req.CreatedBy); err != nil {
		writeMastersError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"status": "recorded"})
}
