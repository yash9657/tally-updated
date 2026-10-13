package masters

// currency.go — FOREIGN CURRENCIES AND THEIR RATES (feature T8, migration 0080).
//
// The design memo is docs/design/t8-multi-currency-model.md. Two of its
// decisions are load-bearing here and are restated because a reader of this file
// cannot re-derive them:
//
// ⛔⛔ THE COMPANY'S OWN BASE CURRENCY IS NOT A ROW IN THIS TABLE. A
// base-currency amount is recorded with currency_id IS NULL, so a master for the
// base currency would be a SECOND encoding of the same fact — and a voucher
// keyed against it would carry rate 1.0 for ever and sit in the forex report
// showing a permanently zero difference, indistinguishable from a genuine
// foreign bill that happens to be flat. A database trigger refuses it; this file
// turns that refusal into a sentence an operator can act on.
//
// ⛔ A RATE IS NOT MONEY. Money is fixed-4 end to end; a rate is NUMERIC(19,10)
// quoted as BASE UNITS PER ONE FOREIGN UNIT (1 USD = 83.65 INR). It is carried
// here as a decimal.Decimal and emitted as a STRING, never a float — see
// HANDOFF.md §9's standing rule, which this file is a fresh chance to break.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/platform/db"
)

// ErrCurrencyIsBase is returned when a currency master is asked for with the
// company's own base currency code.
//
// ⭐ A DISTINCT ERROR RATHER THAN THE DATABASE'S SENTENCE, because the operator's
// next action is different from every other refusal on this screen: there is
// nothing to rename and nothing to delete. They already have this currency — it
// is the one the books are kept in.
var ErrCurrencyIsBase = errors.New(
	"that is this company's base currency, and the books are already kept in it. " +
		"Add a currency only for money you receive or pay in something else")

// Currency is a foreign currency master.
type Currency struct {
	ID            uuid.UUID
	Code          string
	Symbol        string
	FormalName    string
	DecimalPlaces int
}

// CurrencyRecord is a currency as stored, for a list or a picker.
type CurrencyRecord struct {
	ID            uuid.UUID
	Code          string
	Symbol        string
	FormalName    string
	DecimalPlaces int
	// LatestRate is the most recent rate on file and the date it took effect,
	// or empty when no rate has ever been entered.
	//
	// ⚠️ SERVED WITH THE LIST DELIBERATELY: a currency with no rate cannot be
	// used to key a voucher, and a picker that did not say so would offer a
	// choice that fails at the next step for a reason the operator cannot see.
	LatestRate     string
	LatestRateDate string
}

// CreateCurrency creates a foreign currency master and appends a master_audit
// row.
func (s *Service) CreateCurrency(ctx context.Context, c Currency, createdBy string) (uuid.UUID, error) {
	code := strings.TrimSpace(c.Code)
	if code == "" {
		return uuid.Nil, errors.New("masters: currency code is required")
	}
	if strings.TrimSpace(c.Symbol) == "" {
		return uuid.Nil, errors.New("masters: currency symbol is required")
	}
	if strings.TrimSpace(c.FormalName) == "" {
		return uuid.Nil, errors.New("masters: currency formal name is required")
	}
	// ⚠️ REFUSED HERE AS WELL AS BY THE COLUMN'S CHECK. Money is fixed-4 on the
	// wire, so a currency claiming more decimals than that is one this product
	// cannot carry — and saying so in words beats a constraint name.
	if c.DecimalPlaces < 0 || c.DecimalPlaces > 4 {
		return uuid.Nil, fmt.Errorf(
			"masters: %d decimal places: money is carried to 4 decimals in this product, "+
				"so a currency may have between 0 and 4", c.DecimalPlaces)
	}
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return uuid.Nil, db.ErrNoCompany
	}
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("masters: new currency id: %w", err)
	}
	err = s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`INSERT INTO currency (id, company_id, code, symbol, formal_name, decimal_places)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			id, companyID, code, strings.TrimSpace(c.Symbol),
			strings.TrimSpace(c.FormalName), c.DecimalPlaces,
		); err != nil {
			// ⛔ THE TRIGGER'S REFUSAL IS TRANSLATED, NOT PASSED THROUGH. Its
			// message explains the ENCODING (a second meaning for NULL), which is
			// true and useless to the person at the keyboard.
			if isBaseCurrencyRefusal(err) {
				return fmt.Errorf("%s: %w", code, ErrCurrencyIsBase)
			}
			return nameTakenNormalised(err,
				"currency_company_code_normalised", "currency", code, "insert currency")
		}
		after := map[string]any{
			"code": code, "symbol": c.Symbol,
			"formal_name": c.FormalName, "decimal_places": c.DecimalPlaces,
		}
		return appendAudit(ctx, tx, companyID, "currency", id, OpCreate, nil, after, createdBy)
	})
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// isBaseCurrencyRefusal recognises currency_is_not_base()'s RAISE.
//
// ⚠️ MATCHED ON THE MESSAGE because a plpgsql RAISE carries SQLSTATE P0001 and
// no constraint name — there is nothing else to key on. The trigger's message is
// therefore part of its contract, and migration 0080 says so beside the RAISE.
func isBaseCurrencyRefusal(err error) bool {
	return err != nil && strings.Contains(err.Error(), "this is the company base currency")
}

// CurrencyIDByCode resolves a currency case-insensitively.
func (s *Service) CurrencyIDByCode(ctx context.Context, code string) (uuid.UUID, string, error) {
	return s.resolveNormalised(ctx, "currency", "code", code)
}

// ListCurrencies enumerates a company's foreign currencies with the latest rate
// on file for each.
func (s *Service) ListCurrencies(ctx context.Context) ([]CurrencyRecord, error) {
	out := []CurrencyRecord{}
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// DISTINCT ON picks the newest rate per currency in one pass; the LEFT
		// JOIN keeps a currency with NO rate in the list, because hiding it would
		// make "I created USD and it vanished" the operator's experience.
		rows, err := tx.Query(ctx,
			`SELECT c.id, c.code, c.symbol, c.formal_name, c.decimal_places,
			        COALESCE(r.rate::text, ''), COALESCE(to_char(r.effective_date,'YYYY-MM-DD'), '')
			   FROM currency c
			   LEFT JOIN LATERAL (
			        SELECT rate, effective_date FROM currency_rate cr
			         WHERE cr.currency_id = c.id
			         ORDER BY cr.effective_date DESC
			         LIMIT 1
			   ) r ON true
			  ORDER BY c.code`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r CurrencyRecord
			if err := rows.Scan(&r.ID, &r.Code, &r.Symbol, &r.FormalName,
				&r.DecimalPlaces, &r.LatestRate, &r.LatestRateDate); err != nil {
				return err
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// SetExchangeRate records the rate for a currency on a date, replacing any rate
// already on file for that same date.
//
// ⭐ REPLACING IS SAFE, AND ONLY BECAUSE OF Q1's DESIGN. A voucher stores its OWN
// rate, so this table is read for DEFAULTS and for the revaluation report and
// never joined by the ledger — correcting a rate therefore CANNOT restate a
// posted voucher. If the ledger read this table instead, this upsert would
// silently move every foreign voucher ever posted and the Balance Sheet with it.
//
// ⛔ The rate is taken as a STRING and parsed with decimal.NewFromString. Never
// float64: ParseFloat accepts what a rate must not lose, and accepts "NaN" and
// "Inf", which panic inside the decimal package as an opaque 500.
func (s *Service) SetExchangeRate(ctx context.Context, code, effectiveDate, rate, createdBy string) error {
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return db.ErrNoCompany
	}
	d, err := decimal.NewFromString(strings.TrimSpace(rate))
	if err != nil {
		return fmt.Errorf("masters: rate %q is not a number", rate)
	}
	if !d.IsPositive() {
		// A zero rate makes every amount in the currency worth nothing; a negative
		// one flips a sign that money in this system never carries.
		return fmt.Errorf("masters: rate %s: an exchange rate must be greater than zero", d.String())
	}
	currencyID, stored, err := s.CurrencyIDByCode(ctx, code)
	if err != nil {
		return err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("masters: new rate id: %w", err)
	}
	return s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`INSERT INTO currency_rate (id, company_id, currency_id, effective_date, rate)
			 VALUES ($1, $2, $3, $4::date, $5)
			 ON CONFLICT (company_id, currency_id, effective_date)
			 DO UPDATE SET rate = EXCLUDED.rate`,
			id, companyID, currencyID, effectiveDate, d,
		); err != nil {
			return fmt.Errorf("masters: set exchange rate: %w", err)
		}
		after := map[string]any{"code": stored, "effective_date": effectiveDate, "rate": d.String()}
		return appendAudit(ctx, tx, companyID, "currency_rate", id, OpCreate, nil, after, createdBy)
	})
}

// RateAsOn returns the rate in force for a currency on a date — the most recent
// rate whose effective date is on or before it.
//
// ⛔ IT IS NOT AN ERROR FOR THERE TO BE NO RATE, and this returns ok=false rather
// than a zero. A zero rate would value the whole holding at nothing, and it would
// do it silently: every derived figure stays a plausible number.
func (s *Service) RateAsOn(ctx context.Context, code, asOn string) (rate decimal.Decimal, ok bool, err error) {
	currencyID, _, err := s.CurrencyIDByCode(ctx, code)
	if err != nil {
		return decimal.Zero, false, err
	}
	err = s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		scanErr := tx.QueryRow(ctx,
			`SELECT rate FROM currency_rate
			  WHERE currency_id = $1 AND effective_date <= $2::date
			  ORDER BY effective_date DESC
			  LIMIT 1`, currencyID, asOn).Scan(&rate)
		if errors.Is(scanErr, pgx.ErrNoRows) {
			return nil // ok stays false
		}
		if scanErr != nil {
			return scanErr
		}
		ok = true
		return nil
	})
	if err != nil {
		return decimal.Zero, false, fmt.Errorf("masters: rate as on %s: %w", asOn, err)
	}
	return rate, ok, nil
}
