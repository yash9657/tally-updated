package ledger

// forex_voucher_test.go — T8 STEP 4: FOREIGN ENTRY ON A VOUCHER, END TO END.
//
// forex_schema_test.go pins what migration 0080's COLUMNS do. This file pins
// what the WRITE PATH does with them, which until this commit was nothing:
// `INSERT INTO posting (…, dr_cr, amount)` named no currency column, so every
// foreign figure an operator could key was discarded between the request and
// the row.
//
// ⛔⛔ THE ORACLE, INHERITED FROM forex_schema_test.go's HEADER AND NOT SOFTENED:
// a forex test whose only assertion is that the voucher POSTED proves nothing.
// Base, foreign and rate are THREE stored values and TWO of them can be wrong
// while the voucher balances perfectly. So every assertion below names the
// SPECIFIC value it is pinning, and the failure message says which of the three
// was wrong and what it was — never "the voucher did not post".
//
// ⛔ AND THE THREE FIGURES ARE DELIBERATELY MUTUALLY UNMISTAKABLE. base
// 83,651.23 / foreign 1,000.00 / rate 83.6512345678 cannot be confused for one
// another by any mix-up: a rate written into the foreign column, or a foreign
// amount into the base one, changes the number this file reports. Round figures
// (1000 / 1000 / 1.0) would let three different bugs all pass.
//
// ⚠️ AND THE RATE HAS TEN SIGNIFICANT DECIMALS ON PURPOSE. `Money` is fixed-4
// everywhere in this product; a rate is NUMERIC(19,10). Any layer that treats
// the rate as money — a StringFixed(4), a parseFixed4 — truncates it to
// 83.6512, and the ONLY way to see that is to send a rate whose fifth decimal
// onward is non-zero and assert it comes back whole.

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/platform/db"
)

// The three figures of the specimen foreign purchase, named once.
//
// base ≠ foreign × rate exactly (1000 × 83.6512345678 = 83,651.2345678, which
// is not representable in fixed-4 money). That is not sloppiness in the
// fixture, it is the POINT of Q2: the base amount is STORED and authoritative,
// never re-derived from the other two. A write path that recomputed base from
// foreign × rate would post 83,651.2346 here and this file would say so.
const (
	forexBase    = "83651.23"
	forexForeign = "1000.00"
	forexRate    = "83.6512345678"
)

type forexVoucherEnv struct {
	pool    *db.Pool
	ctx     context.Context
	company uuid.UUID
	led     *Service
	ids     *identity.Service

	creditor uuid.UUID
	purchase uuid.UUID
	vtID     uuid.UUID
	usd      uuid.UUID
}

func setupForexVoucher(t *testing.T) *forexVoucherEnv {
	t.Helper()
	pool, companyID, ctx := forexEnv(t)

	e := &forexVoucherEnv{
		pool:    pool,
		ctx:     ctx,
		company: companyID,
		led:     NewService(pool),
		ids:     identity.NewService(pool),
	}
	var err error
	if e.creditor, err = e.ids.CreateLedger(ctx, "Acme Inc (USA)", "Sundry Creditors"); err != nil {
		t.Fatalf("create creditor: %v", err)
	}
	if e.purchase, err = e.ids.CreateLedger(ctx, "Imported Purchases", "Purchase Accounts"); err != nil {
		t.Fatalf("create purchase ledger: %v", err)
	}
	if e.vtID, err = e.ids.VoucherTypeID(ctx, "Purchase"); err != nil {
		t.Fatalf("purchase voucher type: %v", err)
	}
	e.usd = newCurrency(t, pool, ctx, companyID, "USD", "$", "US Dollars")
	return e
}

// foreignPurchase is the specimen: a $1,000 bill from a US supplier, booked at
// 83.6512345678, carrying a bill reference so BOTH tables that gained the three
// columns are exercised by one voucher.
//
// ⚠️ ONLY THE PARTY LEG IS FOREIGN, AND THAT IS THE REALISTIC SHAPE, NOT A
// SIMPLIFICATION. The expense leg is booked in the books' own currency: what
// the company owes is $1,000, what the purchase cost the business is
// ₹83,651.23. A voucher where every leg carried a currency would hide the case
// this file most needs to cover — a MIXED voucher, where one posting is foreign
// and its counterpart is base, which is exactly where a write path that stores
// the currency per-VOUCHER rather than per-POSTING produces a wrong row.
func (e *forexVoucherEnv) foreignPurchase(voucherNo string) VoucherInput {
	base := decimal.RequireFromString(forexBase)
	return VoucherInput{
		Class:         Purchase,
		VoucherTypeID: e.vtID,
		PartyLedgerID: e.creditor,
		VoucherNo:     voucherNo,
		VoucherDate:   "2025-06-10",
		Party:         "Acme Inc (USA)",
		CreatedBy:     "forex-test",
		Postings: []PostingInput{
			{LedgerID: e.purchase, DrCr: Debit, Amount: base},
			{
				LedgerID:      e.creditor,
				DrCr:          Credit,
				Amount:        base,
				CurrencyID:    e.usd,
				ForeignAmount: decimal.RequireFromString(forexForeign),
				Rate:          decimal.RequireFromString(forexRate),
			},
		},
		BillAllocations: []BillAllocationInput{{
			LedgerID:      e.creditor,
			RefName:       voucherNo,
			RefType:       RefNew,
			DrCr:          Credit,
			Amount:        base,
			DueDate:       "2025-07-10",
			CurrencyID:    e.usd,
			ForeignAmount: decimal.RequireFromString(forexForeign),
			Rate:          decimal.RequireFromString(forexRate),
		}},
	}
}

// storedTriple is the three values AS THE DATABASE HOLDS THEM, read as text so
// the SCALE is visible: a rate quantised to 83.6512 and a rate stored whole are
// two different strings here and the same float everywhere else.
type storedTriple struct {
	amount   string
	currency *uuid.UUID
	foreign  *string
	rate     *string
}

func (e *forexVoucherEnv) postingTriples(t *testing.T, voucherID uuid.UUID) map[uuid.UUID]storedTriple {
	t.Helper()
	return e.triples(t, `
		SELECT p.ledger_id, p.amount::text, p.currency_id,
		       p.foreign_amount::text, p.rate::text
		  FROM posting p
		  JOIN voucher_head vh ON vh.current_version_id = p.voucher_version_id
		 WHERE vh.voucher_id = $1`, voucherID)
}

func (e *forexVoucherEnv) billTriples(t *testing.T, voucherID uuid.UUID) map[uuid.UUID]storedTriple {
	t.Helper()
	return e.triples(t, `
		SELECT b.ledger_id, b.amount::text, b.currency_id,
		       b.foreign_amount::text, b.rate::text
		  FROM bill_allocation b
		  JOIN voucher_head vh ON vh.current_version_id = b.voucher_version_id
		 WHERE vh.voucher_id = $1`, voucherID)
}

func (e *forexVoucherEnv) triples(t *testing.T, sql string, voucherID uuid.UUID) map[uuid.UUID]storedTriple {
	t.Helper()
	out := map[uuid.UUID]storedTriple{}
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, sql, voucherID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var lid uuid.UUID
			var st storedTriple
			if err := rows.Scan(&lid, &st.amount, &st.currency, &st.foreign, &st.rate); err != nil {
				return err
			}
			out[lid] = st
		}
		return rows.Err()
	}); err != nil {
		t.Fatalf("read stored triples: %v", err)
	}
	return out
}

// assertForeignTriple names each of the three values separately. A single
// combined assertion would report "the row is wrong" for three different bugs.
func assertForeignTriple(t *testing.T, where string, got storedTriple, wantCurrency uuid.UUID) {
	t.Helper()
	if got.amount != "83651.2300" {
		t.Errorf("%s: THE BASE AMOUNT is %s, want 83651.2300 — posting.amount is the "+
			"figure check_double_entry sums and the Balance Sheet reads; Q2 forbids "+
			"touching it", where, got.amount)
	}
	switch {
	case got.currency == nil:
		t.Errorf("%s: THE CURRENCY is NULL, want %s — NULL means BASE, so this row "+
			"claims a rupee bill", where, wantCurrency)
	case *got.currency != wantCurrency:
		t.Errorf("%s: THE CURRENCY is %s, want %s", where, *got.currency, wantCurrency)
	}
	switch {
	case got.foreign == nil:
		t.Errorf("%s: THE FOREIGN AMOUNT is NULL, want 1000.0000", where)
	case *got.foreign != "1000.0000":
		t.Errorf("%s: THE FOREIGN AMOUNT is %s, want 1000.0000", where, *got.foreign)
	}
	switch {
	case got.rate == nil:
		t.Errorf("%s: THE RATE is NULL, want 83.6512345678", where)
	case *got.rate != "83.6512345678":
		t.Errorf("%s: THE RATE is %s, want 83.6512345678 — a rate is NUMERIC(19,10) "+
			"and 83.6512 is what it becomes when some layer treats it as fixed-4 "+
			"money", where, *got.rate)
	}
}

func assertBaseTriple(t *testing.T, where string, got storedTriple) {
	t.Helper()
	if got.currency != nil || got.foreign != nil || got.rate != nil {
		t.Errorf("%s: expected a BASE row (all three NULL), got currency=%v "+
			"foreign=%v rate=%v — a base posting has exactly ONE encoding",
			where, got.currency, got.foreign, got.rate)
	}
}

// ---------------------------------------------------------------------------
// 1. The write path stores all three, on BOTH tables, and leaves base alone.
// ---------------------------------------------------------------------------

func TestForeignVoucherStoresAllThreeValuesOnBothTables(t *testing.T) {
	e := setupForexVoucher(t)

	res, err := e.led.PostVoucher(e.ctx, e.foreignPurchase("FX-1"))
	if err != nil {
		t.Fatalf("post foreign purchase: %v", err)
	}

	pt := e.postingTriples(t, res.VoucherID)
	if len(pt) != 2 {
		t.Fatalf("expected 2 postings, got %d", len(pt))
	}
	assertForeignTriple(t, "posting[creditor]", pt[e.creditor], e.usd)

	// ⛔ THE MIXED-VOUCHER HALF, AND IT IS NOT DECORATION. The expense leg of
	// this very voucher must remain BASE. A write path that hung the currency
	// off the voucher instead of the posting passes the assertion above and
	// fails this one — and would silently retag every rupee expense line on
	// every foreign invoice in the book.
	assertBaseTriple(t, "posting[purchase] (the counterpart leg of the SAME voucher)", pt[e.purchase])

	// ⛔ AND bill_allocation SEPARATELY. Outstandings reads the allocation, not
	// the posting: columns filled on posting alone would show a correct foreign
	// figure in the ledger and a base-only bill on the ageing report.
	bt := e.billTriples(t, res.VoucherID)
	if len(bt) != 1 {
		t.Fatalf("expected 1 bill allocation, got %d", len(bt))
	}
	assertForeignTriple(t, "bill_allocation[creditor]", bt[e.creditor], e.usd)
}

// ---------------------------------------------------------------------------
// 2. THE CONTROL. An ordinary rupee voucher is untouched by all of this.
// ---------------------------------------------------------------------------

// TestBaseVoucherStillPostsWithNoCurrencyAtAll is the control the house
// standard requires, and it is not a formality: every posting already in every
// book is base, so a write path that wrote a non-NULL currency_id — even a zero
// uuid — would move 7,000-odd existing-shaped rows into "foreign" and put the
// whole book in the unadjusted-forex report.
func TestBaseVoucherStillPostsWithNoCurrencyAtAll(t *testing.T) {
	e := setupForexVoucher(t)

	base := decimal.RequireFromString("5000.00")
	res, err := e.led.PostVoucher(e.ctx, VoucherInput{
		Class:         Purchase,
		VoucherTypeID: e.vtID,
		PartyLedgerID: e.creditor,
		VoucherNo:     "BASE-1",
		VoucherDate:   "2025-06-10",
		CreatedBy:     "forex-test",
		Postings: []PostingInput{
			{LedgerID: e.purchase, DrCr: Debit, Amount: base},
			{LedgerID: e.creditor, DrCr: Credit, Amount: base},
		},
		BillAllocations: []BillAllocationInput{{
			LedgerID: e.creditor, RefName: "BASE-1", RefType: RefNew,
			DrCr: Credit, Amount: base,
		}},
	})
	if err != nil {
		t.Fatalf("control: an ordinary rupee purchase must still post: %v", err)
	}
	for lid, got := range e.postingTriples(t, res.VoucherID) {
		assertBaseTriple(t, "control posting "+lid.String(), got)
	}
	for lid, got := range e.billTriples(t, res.VoucherID) {
		assertBaseTriple(t, "control bill_allocation "+lid.String(), got)
	}
}

// ---------------------------------------------------------------------------
// 3. THE READ-BACK. An alter that echoes the GET must not silently de-currency
//    the voucher — with the inverse control that proves the test can fail.
// ---------------------------------------------------------------------------

// TestForeignVoucherSurvivesAnAlterRoundTrip is the SEVENTH occurrence of the
// defect VoucherView.CostAllocations, .Fulfilments and .GSTLines each record in
// their own comments, and it is the reason this test exists rather than being
// left to the screens.
//
// AN ALTER RE-STATES. `posting` is rewritten from the request body on every
// version, so a GET that does not PROJECT the currency gives a client nothing
// to send back, and the ordinary round trip — open a foreign invoice, fix a
// typo in the narration, save — rewrites a $1,000 bill as a ₹83,651.23 rupee
// bill. ⛔ NOTHING REFUSES IT: three NULLs is a legal state, the voucher still
// balances in base, the Balance Sheet does not move, and the only trace is a
// foreign bill that quietly stops being foreign.
func TestForeignVoucherSurvivesAnAlterRoundTrip(t *testing.T) {
	e := setupForexVoucher(t)

	res, err := e.led.PostVoucher(e.ctx, e.foreignPurchase("FX-2"))
	if err != nil {
		t.Fatalf("post: %v", err)
	}

	// Rebuild the alter body FROM THE GET, exactly as a form re-seeded from a
	// fetched voucher does. Nothing here comes from the fixture — that is the
	// whole point: if the view drops a value, the rebuilt body cannot carry it.
	view, err := e.led.GetVoucher(e.ctx, res.VoucherID)
	if err != nil {
		t.Fatalf("get voucher: %v", err)
	}
	in := e.foreignPurchase("FX-2")
	in.Narration = "corrected the narration and nothing else"
	in.Postings = nil
	for _, p := range view.Postings {
		in.Postings = append(in.Postings, PostingInput{
			LedgerID:      p.LedgerID,
			DrCr:          DrCr(p.DrCr),
			Amount:        p.Amount,
			CurrencyID:    p.CurrencyID,
			ForeignAmount: p.ForeignAmount,
			Rate:          p.Rate,
		})
	}
	in.BillAllocations = nil
	for _, b := range view.BillAllocations {
		in.BillAllocations = append(in.BillAllocations, BillAllocationInput{
			LedgerID:      b.LedgerID,
			RefName:       b.RefName,
			RefType:       RefType(b.RefType),
			DrCr:          DrCr(b.DrCr),
			Amount:        b.Amount,
			DueDate:       b.DueDate,
			CurrencyID:    b.CurrencyID,
			ForeignAmount: b.ForeignAmount,
			Rate:          b.Rate,
		})
	}
	if _, err := e.led.AlterVoucher(e.ctx, res.VoucherID, in); err != nil {
		t.Fatalf("alter: %v", err)
	}

	assertForeignTriple(t, "posting[creditor] AFTER an alter rebuilt from the GET",
		e.postingTriples(t, res.VoucherID)[e.creditor], e.usd)
	assertForeignTriple(t, "bill_allocation[creditor] AFTER an alter rebuilt from the GET",
		e.billTriples(t, res.VoucherID)[e.creditor], e.usd)
}

// TestAnAlterThatOmitsTheCurrencyDoesDestroyIt is the INVERSE CONTROL for the
// test directly above, and without it that test proves nothing.
//
// ⛔ "IT SURVIVED" IS EQUALLY CONSISTENT WITH THE ROWS BEING IMMUNE TO AN
// ALTER. If postings were somehow carried forward rather than rewritten, the
// round-trip test would pass while the projection it claims to guard did not
// exist. This test shows the data IS destructible — so the survival above is
// the projection doing work, and not an accident of the storage engine.
func TestAnAlterThatOmitsTheCurrencyDoesDestroyIt(t *testing.T) {
	e := setupForexVoucher(t)

	res, err := e.led.PostVoucher(e.ctx, e.foreignPurchase("FX-3"))
	if err != nil {
		t.Fatalf("post: %v", err)
	}

	in := e.foreignPurchase("FX-3")
	for i := range in.Postings {
		in.Postings[i].CurrencyID = uuid.Nil
		in.Postings[i].ForeignAmount = decimal.Zero
		in.Postings[i].Rate = decimal.Zero
	}
	for i := range in.BillAllocations {
		in.BillAllocations[i].CurrencyID = uuid.Nil
		in.BillAllocations[i].ForeignAmount = decimal.Zero
		in.BillAllocations[i].Rate = decimal.Zero
	}
	if _, err := e.led.AlterVoucher(e.ctx, res.VoucherID, in); err != nil {
		t.Fatalf("an alter that omits the currency is a LEGAL request (the operator "+
			"may be correcting a voucher that was never foreign); it must be "+
			"accepted, and it must clear the currency: %v", err)
	}
	assertBaseTriple(t, "posting[creditor] after an alter that OMITTED the currency",
		e.postingTriples(t, res.VoucherID)[e.creditor])
}

// ---------------------------------------------------------------------------
// 4. THE HASH. What the edit log shows must be what the edit log signs.
// ---------------------------------------------------------------------------

// TestForeignFiguresRideInTheHashedPayload pins that the three values are part
// of the SIGNATURE and not merely of the row.
//
// ⛔ THE THREAT IS NOT A TYPO, IT IS A TAMPER. If the rate lived only in the
// posting row, someone with database access could restate a $1,000 bill from
// 83.65 to 90.00 and VerifyChain would keep reporting the book as intact —
// the hash covers the base amount, which does not move. The rate is the value
// an audit most needs to be able to trust, so it must be inside the signature.
func TestForeignFiguresRideInTheHashedPayload(t *testing.T) {
	e := setupForexVoucher(t)

	res, err := e.led.PostVoucher(e.ctx, e.foreignPurchase("FX-4"))
	if err != nil {
		t.Fatalf("post: %v", err)
	}

	var payload string
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT vv.payload::text
			  FROM voucher_head vh
			  JOIN voucher_version vv ON vv.id = vh.current_version_id
			 WHERE vh.voucher_id = $1`, res.VoucherID).Scan(&payload)
	}); err != nil {
		t.Fatalf("read payload: %v", err)
	}

	// The RATE, at full scale, by value. Checked as a substring of the signed
	// JSON rather than through the struct, because the struct is what would be
	// wrong: a StringFixed(4) here compiles, round-trips through its own type
	// perfectly, and writes 83.6512 into the signature for ever.
	if !strings.Contains(payload, forexRate) {
		t.Errorf("THE RATE is not in the signed payload at full scale (want %s). "+
			"payload=%s", forexRate, payload)
	}
	if !strings.Contains(payload, e.usd.String()) {
		t.Errorf("THE CURRENCY (%s) is not in the signed payload. payload=%s",
			e.usd, payload)
	}
	if !strings.Contains(payload, `"1000.0000"`) {
		t.Errorf("THE FOREIGN AMOUNT is not in the signed payload. payload=%s", payload)
	}

	// ⛔⛔ AND THE CHAIN MUST STILL VERIFY. VoucherHistory decodes the stored
	// JSONB back into CanonicalPayload and RE-MARSHALS it — Postgres does not
	// preserve a jsonb column's input text, so the hashed bytes have to be
	// re-derived. That re-derivation is exact only while every value is a
	// string. A rate carried as a NUMBER would come back as 83.6512345678 or
	// 8.36512345678e+01 depending on magnitude, and every foreign voucher in
	// the field would report as tampered with — indistinguishable from an
	// actual attack.
	hist, err := e.led.VoucherHistory(e.ctx, res.VoucherID)
	if err != nil {
		t.Fatalf("voucher history: %v", err)
	}
	if !hist.Verification.OK {
		t.Errorf("the chain does not verify over a foreign voucher: %s",
			hist.Verification.Reason)
	}
}

// ---------------------------------------------------------------------------
// 5. THE REFUSALS. Two-of-three, and a rate that is not a rate.
// ---------------------------------------------------------------------------

// TestTwoOfThreeIsRefusedInWordsNotByAConstraintName pins that the Go layer
// says what is wrong.
//
// The database's posting_forex_all_or_none CHECK is the authority and stays the
// authority — but it surfaces as `violates check constraint
// "posting_forex_all_or_none"`, which tells the operator nothing they can act
// on. The refusal must name the missing value.
func TestTwoOfThreeIsRefusedInWordsNotByAConstraintName(t *testing.T) {
	e := setupForexVoucher(t)

	cases := []struct {
		name string
		mut  func(*PostingInput)
		want string
	}{
		{"currency and foreign but no rate", func(p *PostingInput) {
			p.Rate = decimal.Zero
		}, "rate"},
		{"currency and rate but no foreign amount", func(p *PostingInput) {
			p.ForeignAmount = decimal.Zero
		}, "foreign amount"},
		{"a foreign amount with no currency", func(p *PostingInput) {
			p.CurrencyID = uuid.Nil
		}, "currency"},
		{"a negative rate", func(p *PostingInput) {
			p.Rate = decimal.RequireFromString("-83.65")
		}, "rate"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := e.foreignPurchase("FX-R-" + tc.name)
			tc.mut(&in.Postings[1])
			in.BillAllocations = nil
			_, err := e.led.PostVoucher(e.ctx, in)
			if err == nil {
				t.Fatalf("accepted a two-of-three posting; two of the three stored " +
					"values can be wrong while the voucher balances perfectly, which " +
					"is the entire reason the triple is all-or-none")
			}
			if !strings.Contains(strings.ToLower(err.Error()), tc.want) {
				t.Errorf("refused, but the message does not name %q, so the operator "+
					"cannot act on it: %v", tc.want, err)
			}
			if strings.Contains(err.Error(), "check constraint") {
				t.Errorf("refused by the raw constraint rather than in words: %v", err)
			}
		})
	}
}

// TestACurrencyFromAnotherCompanyIsRefused pins the tenancy edge of the new
// foreign key. The composite FK (company_id, currency_id) is the authority;
// this asserts the refusal is reached rather than the row being written under
// this company with someone else's currency id.
func TestACurrencyFromAnotherCompanyIsRefused(t *testing.T) {
	e := setupForexVoucher(t)

	other := newInwardCompany(t, e.pool, "Other Forex Co")
	otherCtx := db.WithCompany(context.Background(), other)
	theirUSD := newCurrency(t, e.pool, otherCtx, other, "USD", "$", "US Dollars")

	in := e.foreignPurchase("FX-X")
	in.Postings[1].CurrencyID = theirUSD
	in.BillAllocations[0].CurrencyID = theirUSD
	if _, err := e.led.PostVoucher(e.ctx, in); err == nil {
		t.Fatal("accepted another company's currency id on a posting")
	}
}
