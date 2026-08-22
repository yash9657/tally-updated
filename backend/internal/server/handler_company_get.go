package server

// GET /v1/companies/{companyID} — read the company master (WS8a).
//
// WHY THIS EXISTS: the company's home GST state (`company.state_code`, added by
// migration 0008) is the SupplierStateCode the ledger falls back to when a post
// leaves it empty (internal/ledger/ledger.go, the atomic default inside the post
// tx). Until this route existed the frontend could WRITE state_code at company
// creation but could never READ it back, so it hardcoded a bootstrap constant
// ("29" = Karnataka) and derived the wrong intra/inter-state split in-browser
// for every non-Karnataka company. This endpoint closes that loop: the client
// hydrates the real state code on load and the client-side split agrees with the
// server-side derivation.
//
// WIRING (two-touch rule, §1.5 of IMPLEMENTATION_PLAN.md): this handler is inert
// until BOTH (a) `r.Get("/", s.handleGetCompany)` is registered inside the
// `/v1/companies/{companyID}` subtree in routes(), and (b) routeAction() maps
// `GET ""`/`GET "/"` to (authz.Display, "company"). Without (b) the default-deny
// middleware 403s even an Owner.
//
// SAFETY: the read runs inside RunInReadOnlyTx, i.e. `BEGIN ... READ ONLY` with
// `app.current_company_id` bound as the first statement. The `company` table is
// FORCE RLS with `USING (company_id = app_current_company_id())`, so the row is
// tenant-scoped by the database independently of the WHERE clause; the `$1`
// placeholder is the middleware-authorized company id (never user text).

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/identity"
)

// companyViewResp is the JSON body of GET /v1/companies/{companyID}. The field
// names MIRROR companyReq (the POST /v1/companies body) exactly, so a client can
// round-trip create -> read without a second mapping table. fy_start_date is a
// SQL DATE, emitted in the same "YYYY-MM-DD" form the create accepts.
type companyViewResp struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	FYStartDate  string `json:"fy_start_date"`
	BaseCurrency string `json:"base_currency"`
	StateCode    string `json:"state_code"`
	GSTIN        string `json:"gstin"`

	// Mailing block + country (migration 0015).
	MailingName string `json:"mailing_name"`
	Address     string `json:"address"`
	City        string `json:"city"`
	Pincode     string `json:"pincode"`
	Country     string `json:"country"`

	// BooksBeginningFrom is distinct from FYStartDate: it is the date the books
	// begin here, and therefore the date opening balances are stated as at.
	BooksBeginningFrom string `json:"books_beginning_from"`

	// Base-currency presentation.
	CurrencySymbol     string `json:"currency_symbol"`
	CurrencyFormalName string `json:"currency_formal_name"`
	DecimalPlaces      int    `json:"decimal_places"`

	// AllowNegativeStock is the inventory escape hatch (migration 0025). It is
	// on the READ for a reason beyond symmetry: until PUT / existed there was
	// no way for a client to learn the value of a setting BOTH stock enforcers
	// were already acting on, so a screen could only ever guess at it -- and
	// FeaturesScreen guessed, correctly, that it was permanently false. A
	// control that cannot read its own state back is the D43 shape again.
	AllowNegativeStock bool `json:"allow_negative_stock"`
}

// handleGetCompany returns the company master for the authorized company.
func (s *Server) handleGetCompany(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}

	var (
		name         string
		fyStart      time.Time
		baseCurrency string
		stateCode    string
		gstin        string

		mailingName string
		address     string
		city        string
		pincode     string
		country     string
		booksFrom   time.Time

		currencySymbol     string
		currencyFormalName string
		decimalPlaces      int

		allowNegativeStock bool
	)
	err := s.pool.RunInReadOnlyTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT name, fy_start_date, base_currency, state_code, gstin,
			        mailing_name, address, city, pincode, country,
			        books_beginning_from,
			        currency_symbol, currency_formal_name, decimal_places,
			        allow_negative_stock
			   FROM company
			  WHERE id = $1`, companyID).
			Scan(&name, &fyStart, &baseCurrency, &stateCode, &gstin,
				&mailingName, &address, &city, &pincode, &country,
				&booksFrom,
				&currencySymbol, &currencyFormalName, &decimalPlaces,
				&allowNegativeStock)
	})
	if err != nil {
		// No row = either the company does not exist or RLS hid it. Both are a
		// 404 to the caller (membership was already proven by the middleware, so
		// this is not an information leak: a non-member never reaches here).
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "company not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "read company: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, companyViewResp{
		ID:           companyID.String(),
		Name:         name,
		FYStartDate:  fyStart.Format("2006-01-02"),
		BaseCurrency: baseCurrency,
		StateCode:    stateCode,
		GSTIN:        gstin,

		MailingName: mailingName,
		Address:     address,
		City:        city,
		Pincode:     pincode,
		Country:     country,

		BooksBeginningFrom: booksFrom.Format("2006-01-02"),

		CurrencySymbol:     currencySymbol,
		CurrencyFormalName: currencyFormalName,
		DecimalPlaces:      decimalPlaces,

		AllowNegativeStock: allowNegativeStock,
	})
}

// --------------------------------------------------------------------------
// PUT /v1/companies/{companyID} — write the company FEATURE settings (D5).
// --------------------------------------------------------------------------

// companyFeaturesReq is the PUT body. One field today, and the shape is chosen
// so a second can be added without breaking a client: every field is a POINTER,
// and an omitted field LEAVES THE SETTING ALONE rather than resetting it to a
// zero value. A settings PUT that silently reverted the settings it was not
// told about would be a worse defect than the one this route closes.
type companyFeaturesReq struct {
	AllowNegativeStock *bool `json:"allow_negative_stock"`
}

// handleUpdateCompany serves PUT /v1/companies/{companyID}.
//
// ⛔ IT ANSWERS WITH THE ROW, NEVER WITH THE REQUEST. Echoing the body back is
// exactly what POST /v1/companies did with this same key for the whole life of
// that route (register row D43): the caller was told a setting had been applied
// while the column never moved, and an echo is structurally incapable of telling
// those two outcomes apart. The read-back here is the same discipline
// handleSetTDSProfile follows for the same reason.
func (s *Server) handleUpdateCompany(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
	var req companyFeaturesReq
	if !decodeJSON(w, r, &req) {
		return
	}

	// Absent field => leave it as it is. That needs the CURRENT value, so read
	// before writing rather than defaulting to false.
	current, err := s.identity.CompanyFeatures(ctx, companyID)
	if err != nil {
		writeCompanyFeaturesError(w, err)
		return
	}
	desired := current
	if req.AllowNegativeStock != nil {
		desired = *req.AllowNegativeStock
	}

	if err := s.identity.SetCompanyFeatures(ctx, companyID, desired); err != nil {
		writeCompanyFeaturesError(w, err)
		return
	}

	stored, err := s.identity.CompanyFeatures(ctx, companyID)
	if err != nil {
		writeCompanyFeaturesError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":                   companyID.String(),
		"allow_negative_stock": stored,
	})
}

// writeCompanyFeaturesError maps identity's errors onto the same status codes
// handleGetCompany uses, so a missing/RLS-hidden company reads 404 on both
// halves of the route rather than 404 on the read and 500 on the write.
func writeCompanyFeaturesError(w http.ResponseWriter, err error) {
	if errors.Is(err, identity.ErrCompanyNotFound) {
		writeError(w, http.StatusNotFound, "company not found")
		return
	}
	writeError(w, http.StatusInternalServerError, "company features: "+err.Error())
}
