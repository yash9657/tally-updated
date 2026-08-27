package ledger

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/platform/testdb"
	"github.com/khata/cored/migrations"
)

// ---------------------------------------------------------------------------
// T8 multi-currency — the SCHEMA invariants of migration 0080.
// ---------------------------------------------------------------------------
//
// ⛔ WHY THESE ARE DATABASE TESTS AND NOT SERVICE TESTS. There is no Go write
// path for a foreign posting yet, and these invariants must hold before one
// exists — they are what the write path will be built against. Every one of
// them was first proved by hand on a scratch database with the failure
// PREDICTED and then observed; this file is what keeps them proved.
//
// ⛔⛔ THE ORACLE THIS FILE EXISTS TO BE. A forex test whose only assertion is
// that the voucher POSTED proves nothing: base, foreign and rate are THREE
// stored values and two of them can be wrong while the voucher balances
// perfectly. So each test names the specific value it is pinning.

func forexEnv(t *testing.T) (*db.Pool, uuid.UUID, context.Context) {
	t.Helper()
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	ctx := context.Background()

	adminConn, err := pgx.Connect(ctx, testdb.AdminDSNValue())
	if err != nil {
		t.Skipf("admin postgres unavailable, skipping integration test: %v", err)
	}
	if err := migrations.Apply(ctx, adminConn); err != nil {
		_ = adminConn.Close(ctx)
		t.Fatalf("apply migrations: %v", err)
	}
	_ = adminConn.Close(ctx)

	pool, err := db.New(ctx, testdb.AppDSN)
	if err != nil {
		t.Fatalf("app pool: %v", err)
	}
	t.Cleanup(pool.Close)

	// Creates the company AND registers testdb.Cleanup in the same breath;
	// internal/testhygiene fails the build for a helper that splits the pair.
	companyID := newInwardCompany(t, pool, "Forex Co")
	return pool, companyID, db.WithCompany(ctx, companyID)
}

// exec runs one statement in a company-scoped transaction. ⭐ Deliberately
// through RunInTx rather than an admin connection: that sets the tenant GUC, so
// these rows are written the way real code writes them, under RLS, rather than
// around it.
func exec(ctx context.Context, pool *db.Pool, sql string, args ...any) error {
	return pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, sql, args...)
		return err
	})
}

// newCurrency inserts a currency master and returns its id.
func newCurrency(t *testing.T, pool *db.Pool, ctx context.Context, cid uuid.UUID, code, symbol, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if err := exec(ctx, pool,
		`INSERT INTO currency (id, company_id, code, symbol, formal_name, decimal_places)
		 VALUES ($1, $2, $3, $4, $5, 2)`, id, cid, code, symbol, name); err != nil {
		t.Fatalf("create currency %s: %v", code, err)
	}
	return id
}

// TestCurrencyMasterRefusesTheCompanyBaseCurrency pins the ONE-ENCODING rule.
//
// A base-currency amount is recorded with currency_id IS NULL. If an INR-base
// company could also hold an 'INR' currency master, "this posting is in rupees"
// would have two encodings, and a voucher keyed against that row would sit in
// the unadjusted-forex report for ever showing a permanently zero difference —
// indistinguishable from a genuine foreign bill that happens to be flat.
func TestCurrencyMasterRefusesTheCompanyBaseCurrency(t *testing.T) {
	pool, cid, ctx := forexEnv(t)

	var base string
	if err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT base_currency FROM company WHERE id = $1`, cid).Scan(&base)
	}); err != nil {
		t.Fatalf("read base currency: %v", err)
	}
	if base != "INR" {
		t.Fatalf("precondition: expected an INR-base company, got %q", base)
	}

	// The CONTROL first: a currency that is NOT the base must be accepted, so a
	// blanket refusal cannot masquerade as this rule working.
	usd := newCurrency(t, pool, ctx, cid, "USD", "$", "US Dollars")
	if usd == uuid.Nil {
		t.Fatal("control: USD should have been accepted")
	}

	for _, code := range []string{"INR", "inr", "  InR  "} {
		err := exec(ctx, pool,
			`INSERT INTO currency (id, company_id, code, symbol, formal_name)
			 VALUES ($1, $2, $3, '₹', 'Indian Rupees')`, uuid.New(), cid, code)
		if err == nil {
			t.Fatalf("currency %q: accepted, but it IS the base currency — that is a "+
				"second encoding of currency_id IS NULL", code)
		}
		if !strings.Contains(err.Error(), "base currency") {
			t.Errorf("currency %q: refused for the wrong reason: %v", code, err)
		}
	}
}

// TestExchangeRateKeepsTenDecimals pins the precision decision.
//
// ⛔ A RATE IS NOT MONEY. Money is fixed-4 end to end; a rate is NUMERIC(19,10)
// quoted as BASE UNITS PER ONE FOREIGN UNIT. At 4dp a weak currency's rate
// quantises and a large invoice moves by a visible amount, and the client's
// parseFixed4 TRUNCATES the fifth decimal — so this asserts the tenth survives.
func TestExchangeRateKeepsTenDecimals(t *testing.T) {
	pool, cid, ctx := forexEnv(t)
	idr := newCurrency(t, pool, ctx, cid, "IDR", "Rp", "Indonesian Rupiah")

	const wanted = "0.0033333333" // 1 IDR in INR — small even quoted base-per-foreign
	if err := exec(ctx, pool,
		`INSERT INTO currency_rate (id, company_id, currency_id, effective_date, rate)
		 VALUES ($1, $2, $3, DATE '2026-08-27', $4::numeric)`,
		uuid.New(), cid, idr, wanted); err != nil {
		t.Fatalf("insert rate: %v", err)
	}

	var got string
	if err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT rate::text FROM currency_rate WHERE company_id = $1 AND currency_id = $2`,
			cid, idr).Scan(&got)
	}); err != nil {
		t.Fatalf("read rate: %v", err)
	}
	if got != wanted {
		t.Errorf("rate round trip: got %q, want %q — the tenth decimal is the point", got, wanted)
	}

	// A rate of zero would make every amount in the currency worth nothing, and a
	// negative one would flip a sign that money in this system never carries.
	for _, bad := range []string{"0", "-83.65"} {
		if err := exec(ctx, pool,
			`INSERT INTO currency_rate (id, company_id, currency_id, effective_date, rate)
			 VALUES ($1, $2, $3, DATE '2026-09-01', $4::numeric)`,
			uuid.New(), cid, idr, bad); err == nil {
			t.Errorf("rate %s was accepted; a rate must be > 0", bad)
		}
	}
}

// TestForexTripleIsAllOrNone pins §5.4's "three stored values, not two plus
// arithmetic" on ALL THREE tables that carry one.
//
// ⛔ ledger_opening_bill IS IN THIS LIST DELIBERATELY. Outstandings nets each
// (ledger, ref_name) across a UNION of bill_allocation AND ledger_opening_bill,
// so a base-only opening bill would net into the same group as the foreign
// allocations that settle it and show a partial foreign figure that no total on
// the report contradicts.
func TestForexTripleIsAllOrNone(t *testing.T) {
	pool, cid, ctx := forexEnv(t)
	usd := newCurrency(t, pool, ctx, cid, "USD", "$", "US Dollars")

	ledgerID, err := ensureLedger(ctx, identity.NewService(pool), "Acme Inc (USA)", "Sundry Debtors")
	if err != nil {
		t.Fatalf("debtor ledger: %v", err)
	}

	// ledger_opening_bill needs no voucher, so it carries the whole matrix.
	cases := []struct {
		name     string
		currency any
		foreign  any
		rate     any
		wantOK   bool
	}{
		{"all three present", usd, "1000.0000", "83.6500000000", true},
		{"all three absent (base)", nil, nil, nil, true},
		{"currency + foreign, NO rate", usd, "1000.0000", nil, false},
		{"currency + rate, NO foreign", usd, nil, "83.6500000000", false},
		{"foreign + rate, NO currency", nil, "1000.0000", "83.6500000000", false},
		{"currency only", usd, nil, nil, false},
		{"foreign only", nil, "1000.0000", nil, false},
		{"rate only", nil, nil, "83.6500000000", false},
	}
	for i, c := range cases {
		err := exec(ctx, pool,
			`INSERT INTO ledger_opening_bill
			   (id, company_id, ledger_id, ref_name, ref_type, dr_cr, amount,
			    currency_id, foreign_amount, rate)
			 VALUES ($1, $2, $3, $4, 'NEW', 'D', 83650.0000, $5, $6::numeric, $7::numeric)`,
			uuid.New(), cid, ledgerID, "OPEN-"+string(rune('A'+i)), c.currency, c.foreign, c.rate)
		switch {
		case c.wantOK && err != nil:
			t.Errorf("%s: refused, want accepted: %v", c.name, err)
		case !c.wantOK && err == nil:
			t.Errorf("%s: ACCEPTED — a two-of-three row lets a report compute a "+
				"plausible wrong number", c.name)
		case !c.wantOK && !strings.Contains(err.Error(), "forex_all_or_none"):
			t.Errorf("%s: refused by the wrong constraint: %v", c.name, err)
		}
	}
}

// TestForeignVoucherBalancesInBaseAndNeedNotInForeign is THE pair that pins
// which amount is authoritative, and neither half proves it alone.
//
//   - a voucher that balances in BASE but NOT in foreign must COMMIT;
//   - a voucher that balances in FOREIGN but NOT in base must be REFUSED.
//
// ⛔ Only the second says WHICH column check_double_entry() actually sums. A
// suite with the first alone would pass just as happily if the trigger had been
// pointed at foreign_amount.
func TestForeignVoucherBalancesInBaseAndNeedNotInForeign(t *testing.T) {
	pool, cid, ctx := forexEnv(t)
	ids := identity.NewService(pool)
	usd := newCurrency(t, pool, ctx, cid, "USD", "$", "US Dollars")
	eur := newCurrency(t, pool, ctx, cid, "EUR", "€", "Euro")

	debtor, err := ensureLedger(ctx, ids, "Acme Inc (USA)", "Sundry Debtors")
	if err != nil {
		t.Fatalf("debtor ledger: %v", err)
	}
	sales, err := ensureLedger(ctx, ids, "Export Sales", "Sales Accounts")
	if err != nil {
		t.Fatalf("sales ledger: %v", err)
	}
	vtID, err := ids.VoucherTypeID(ctx, "Journal")
	if err != nil {
		t.Fatalf("journal voucher type: %v", err)
	}

	// post writes a two-legged voucher inside ONE transaction, because
	// check_double_entry is DEFERRED: it fires at COMMIT, not at INSERT.
	post := func(dr, cr string, drFx, crFx string, drRate, crRate string) error {
		// ⛔ ONE TRANSACTION, because check_double_entry is DEFERRED: it fires at
		// COMMIT, not at INSERT. Two separate statements would each commit alone
		// and the first leg would be refused for being unbalanced by itself.
		return pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			vvID := uuid.New()
			if _, err := tx.Exec(ctx,
				`INSERT INTO voucher_version
				   (id, company_id, voucher_id, version_no, op, node_id, hash, payload,
				    created_by, is_reverse_charge, voucher_type_id, voucher_date)
				 VALUES ($1, $2, $3, 1, 'CREATE', 'test', '\x00', '{}', 'forex-test', false, $4, DATE '2026-08-27')`,
				vvID, cid, uuid.New(), vtID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO posting (id, company_id, voucher_version_id, ledger_id, dr_cr, amount,
				                      currency_id, foreign_amount, rate)
				 VALUES ($1, $2, $3, $4, 'D', $5::numeric, $6, $7::numeric, $8::numeric)`,
				uuid.New(), cid, vvID, debtor, dr, usd, drFx, drRate); err != nil {
				return err
			}
			_, err := tx.Exec(ctx,
				`INSERT INTO posting (id, company_id, voucher_version_id, ledger_id, dr_cr, amount,
				                      currency_id, foreign_amount, rate)
				 VALUES ($1, $2, $3, $4, 'C', $5::numeric, $6, $7::numeric, $8::numeric)`,
				uuid.New(), cid, vvID, sales, cr, eur, crFx, crRate)
			return err
		})
	}

	// ⭐ BASE BALANCES, FOREIGN DOES NOT: $1,000 in against €920 out, both at
	// their own rate, meeting at ₹83,650. A multi-currency journal is not
	// required to balance in any single foreign currency, and this must COMMIT.
	if err := post("83650.0000", "83650.0000", "1000.0000", "920.0000",
		"83.6500000000", "90.9239130435"); err != nil {
		t.Fatalf("base-balanced voucher was REFUSED: %v\n"+
			"a multi-currency journal balances in BASE and need not balance in any foreign currency", err)
	}

	// ⛔ THE MIRROR: foreign balances ($1,000 against $1,000) and base does NOT
	// (₹83,650 against ₹80,000). This is what proves base is the summed column.
	err = post("83650.0000", "80000.0000", "1000.0000", "1000.0000",
		"83.6500000000", "80.0000000000")
	if err == nil {
		t.Fatal("a voucher whose BASE legs disagree was ACCEPTED — check_double_entry " +
			"is not summing posting.amount, and every report built on it is wrong")
	}
	if !strings.Contains(err.Error(), "double-entry violation") {
		t.Errorf("refused for the wrong reason: %v", err)
	}
}
