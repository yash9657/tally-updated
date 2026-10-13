package ledger

// forex_settlement_test.go — T8 STEP 5: REALISED GAIN/LOSS ON SETTLEMENT.
//
// forex_schema_test.go pins what migration 0080's COLUMNS do. forex_voucher_test.go
// pins that the WRITE PATH stores the triple. This file pins what the engine does
// with two rates that DIFFER, which until this commit was nothing: a $1,000 bill
// raised at 83.65 and settled at 85.25 posted happily, realised no difference, and
// the 1,600 rupees the business actually gained appeared in no ledger at all.
//
// ⛔⛔ THE ORACLE, INHERITED AND NOT SOFTENED: a forex test whose only assertion is
// that the voucher POSTED proves nothing. Here that rule has a second edge, because
// ZERO IS WHAT A BROKEN COMPUTATION ALSO RETURNS — a settlement at the same rate as
// the bill realises nothing, and so does a derivation that never ran. So:
//
//   * every settlement below settles at a rate DIFFERENT from the bill's, and
//   * two different settlement rates are used across the file, because one rate lets
//     a hard-coded constant pass, and
//   * every assertion names WHICH VALUE is wrong AND WHICH LEDGER took it. "The
//     voucher did not post" is not a failure message this file is allowed to print.
//
// ⚠️ AND THE RATES CARRY TEN SIGNIFICANT DECIMALS ON BOTH SIDES. The realised figure
// is a DIFFERENCE of two rate-scaled products, which is the one arithmetic where a
// fixed-4 rate is invisible in the inputs and wrong in the output: quantise both
// rates to 4 dp and 1,600.0046 becomes 1,599.9700, a figure that is plausible, lands
// in the right ledger, on the right side, and is wrong by 3.5 paise per lakh for ever.

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/platform/db"
)

// The specimen bill and the two rates it is settled at.
//
// ⛔ base ≠ foreign × rate IN THE FIXTURE, DELIBERATELY, and step 5 is where that
// stops being a curiosity and starts deciding the arithmetic. 1000 × 83.6512345678
// is 83,651.2345678, which fixed-4 money cannot hold; the bill's stored base is
// 83,651.23. The memo's formula `foreign × (r_settle − r_bill)` would derive
// 1,598.7654 here while the books need the receivable to clear at its STORED
// 83,651.23 — so the two disagree by the bill's own rounding residue and the
// voucher cannot balance against a real bank amount. See deriveRealisedForex's
// header for the resolution and why it reduces to the memo's formula exactly when
// the bill's base IS the arithmetic product.
const (
	settleBillBase    = "83651.23"
	settleBillForeign = "1000.00"
	settleBillRate    = "83.6512345678"
	// The settlement-date rate. Higher than the bill's, so a RECEIVABLE gains and
	// a PAYABLE loses, and the sign convention is exercised in both directions.
	settleRate = "85.2512345678"
	// A second, wholly different settlement rate. §6.2's lesson applied before it
	// can bite: with one rate in the whole file a hard-coded constant passes.
	settleRateLow = "79.1234567890"
)

type forexSettleEnv struct {
	pool    *db.Pool
	ctx     context.Context
	company uuid.UUID
	led     *Service
	ids     *identity.Service

	debtor   uuid.UUID
	creditor uuid.UUID
	sales    uuid.UUID
	purchase uuid.UUID
	bank     uuid.UUID
	forexPL  uuid.UUID

	salesVT    uuid.UUID
	purchaseVT uuid.UUID
	receiptVT  uuid.UUID
	paymentVT  uuid.UUID

	usd uuid.UUID
	eur uuid.UUID
}

func setupForexSettle(t *testing.T) *forexSettleEnv {
	t.Helper()
	pool, companyID, ctx := forexEnv(t)

	e := &forexSettleEnv{
		pool:    pool,
		ctx:     ctx,
		company: companyID,
		led:     NewService(pool),
		ids:     identity.NewService(pool),
	}
	mk := func(name, group string) uuid.UUID {
		id, err := e.ids.CreateLedger(ctx, name, group)
		if err != nil {
			t.Fatalf("create ledger %s: %v", name, err)
		}
		return id
	}
	e.debtor = mk("Globex LLC (USA)", "Sundry Debtors")
	e.creditor = mk("Acme Inc (USA)", "Sundry Creditors")
	e.sales = mk("Export Sales", "Sales Accounts")
	e.purchase = mk("Imported Purchases", "Purchase Accounts")
	e.bank = mk("EEFC Bank Account", "Bank Accounts")
	// ⚠️ ONE ledger for both directions, and that is Tally's shape: a realised
	// difference is a single "Forex Gain/Loss" account that a gain CREDITS and a
	// loss DEBITS. Two ledgers would make the sign convention a master-data
	// question instead of an arithmetic one.
	e.forexPL = mk("Forex Gain/Loss", "Indirect Expenses")

	vt := func(name string) uuid.UUID {
		id, err := e.ids.VoucherTypeID(ctx, name)
		if err != nil {
			t.Fatalf("voucher type %s: %v", name, err)
		}
		return id
	}
	e.salesVT, e.purchaseVT = vt("Sales"), vt("Purchase")
	e.receiptVT, e.paymentVT = vt("Receipt"), vt("Payment")

	e.usd = newCurrency(t, pool, ctx, companyID, "USD", "$", "US Dollars")
	e.eur = newCurrency(t, pool, ctx, companyID, "EUR", "E", "Euro")
	return e
}

// chooseForexLedger records the operator's choice. Seeded by raw SQL on purpose:
// this file is about the ENGINE, and routing it through the HTTP setter would make
// every assertion here depend on a route's authorisation. handler_forex_settings_test.go
// owns that half.
func (e *forexSettleEnv) chooseForexLedger(t *testing.T, ledgerID uuid.UUID) {
	t.Helper()
	var arg any
	if ledgerID != uuid.Nil {
		arg = ledgerID
	}
	if err := exec(e.ctx, e.pool,
		`INSERT INTO company_forex_settings (company_id, forex_gain_loss_ledger_id)
		 VALUES ($1, $2)
		 ON CONFLICT (company_id) DO UPDATE SET forex_gain_loss_ledger_id = EXCLUDED.forex_gain_loss_ledger_id`,
		e.company, arg); err != nil {
		t.Fatalf("choose forex ledger: %v", err)
	}
}

// ⚠️ `dec` is item_invoice_test.go's, reused rather than shadowed — one
// spelling of "a decimal literal in a fixture" per package.

// foreignSale is the $1,000 EXPORT INVOICE: a receivable, so a rising rate GAINS.
func (e *forexSettleEnv) foreignSale(no string) VoucherInput {
	base := dec(settleBillBase)
	return VoucherInput{
		Class:         Sales,
		VoucherTypeID: e.salesVT,
		PartyLedgerID: e.debtor,
		VoucherNo:     no,
		VoucherDate:   "2025-06-10",
		Party:         "Globex LLC (USA)",
		CreatedBy:     "forex-settle-test",
		Postings: []PostingInput{
			{LedgerID: e.sales, DrCr: Credit, Amount: base},
			{
				LedgerID: e.debtor, DrCr: Debit, Amount: base,
				CurrencyID: e.usd, ForeignAmount: dec(settleBillForeign), Rate: dec(settleBillRate),
			},
		},
		BillAllocations: []BillAllocationInput{{
			LedgerID: e.debtor, RefName: no, RefType: RefNew, DrCr: Debit, Amount: base,
			DueDate:    "2025-07-10",
			CurrencyID: e.usd, ForeignAmount: dec(settleBillForeign), Rate: dec(settleBillRate),
		}},
	}
}

// foreignPurchaseBill is the $1,000 IMPORT INVOICE: a payable, so a rising rate LOSES.
func (e *forexSettleEnv) foreignPurchaseBill(no string) VoucherInput {
	base := dec(settleBillBase)
	return VoucherInput{
		Class:         Purchase,
		VoucherTypeID: e.purchaseVT,
		PartyLedgerID: e.creditor,
		VoucherNo:     no,
		VoucherDate:   "2025-06-10",
		Party:         "Acme Inc (USA)",
		CreatedBy:     "forex-settle-test",
		Postings: []PostingInput{
			{LedgerID: e.purchase, DrCr: Debit, Amount: base},
			{
				LedgerID: e.creditor, DrCr: Credit, Amount: base,
				CurrencyID: e.usd, ForeignAmount: dec(settleBillForeign), Rate: dec(settleBillRate),
			},
		},
		BillAllocations: []BillAllocationInput{{
			LedgerID: e.creditor, RefName: no, RefType: RefNew, DrCr: Credit, Amount: base,
			DueDate:    "2025-07-10",
			CurrencyID: e.usd, ForeignAmount: dec(settleBillForeign), Rate: dec(settleBillRate),
		}},
	}
}

// receipt settles `foreign` of the named receivable at `rate`, clearing `baseCleared`
// of the bill's book value and taking `realised` to the forex ledger.
//
// ⚠️ EVERY FIGURE IS PASSED IN RATHER THAN DERIVED HERE. A helper that computed the
// realised amount with the same expression the engine uses would assert that the
// engine agrees with itself; these tests state the number a human worked out.
func (e *forexSettleEnv) receipt(no, ref, foreign, rate, baseCleared, realised string, gain bool) VoucherInput {
	bankSide := dec(baseCleared).Add(dec(realised))
	if !gain {
		bankSide = dec(baseCleared).Sub(dec(realised))
	}
	in := VoucherInput{
		Class:         Receipt,
		VoucherTypeID: e.receiptVT,
		PartyLedgerID: e.debtor,
		VoucherNo:     no,
		VoucherDate:   "2025-09-15",
		Party:         "Globex LLC (USA)",
		CreatedBy:     "forex-settle-test",
		Postings: []PostingInput{
			{LedgerID: e.bank, DrCr: Debit, Amount: bankSide},
			{
				LedgerID: e.debtor, DrCr: Credit, Amount: dec(baseCleared),
				CurrencyID: e.usd, ForeignAmount: dec(foreign), Rate: dec(rate),
			},
		},
		BillAllocations: []BillAllocationInput{{
			LedgerID: e.debtor, RefName: ref, RefType: RefAgainst, DrCr: Credit,
			Amount:     dec(baseCleared),
			CurrencyID: e.usd, ForeignAmount: dec(foreign), Rate: dec(rate),
		}},
	}
	if !dec(realised).IsZero() {
		side := Credit // a gain on a receivable is income
		if !gain {
			side = Debit
		}
		in.Postings = append(in.Postings,
			PostingInput{LedgerID: e.forexPL, DrCr: side, Amount: dec(realised)})
	}
	return in
}

// payment settles `foreign` of the named PAYABLE at `rate`.
func (e *forexSettleEnv) payment(no, ref, foreign, rate, baseCleared, realised string, gain bool) VoucherInput {
	bankSide := dec(baseCleared).Add(dec(realised))
	if gain {
		bankSide = dec(baseCleared).Sub(dec(realised))
	}
	in := VoucherInput{
		Class:         Payment,
		VoucherTypeID: e.paymentVT,
		PartyLedgerID: e.creditor,
		VoucherNo:     no,
		VoucherDate:   "2025-09-15",
		Party:         "Acme Inc (USA)",
		CreatedBy:     "forex-settle-test",
		Postings: []PostingInput{
			{LedgerID: e.bank, DrCr: Credit, Amount: bankSide},
			{
				LedgerID: e.creditor, DrCr: Debit, Amount: dec(baseCleared),
				CurrencyID: e.usd, ForeignAmount: dec(foreign), Rate: dec(rate),
			},
		},
		BillAllocations: []BillAllocationInput{{
			LedgerID: e.creditor, RefName: ref, RefType: RefAgainst, DrCr: Debit,
			Amount:     dec(baseCleared),
			CurrencyID: e.usd, ForeignAmount: dec(foreign), Rate: dec(rate),
		}},
	}
	if !dec(realised).IsZero() {
		side := Debit // a loss on a payable is an expense
		if gain {
			side = Credit
		}
		in.Postings = append(in.Postings,
			PostingInput{LedgerID: e.forexPL, DrCr: side, Amount: dec(realised)})
	}
	return in
}

// ledgerPosting reads the amount and side actually stored against one ledger on one
// voucher. ⛔ THE ORACLE IS THE ROW, NOT THE RETURN VALUE of PostVoucher: a service
// that computed the right number and wrote it nowhere passes any assertion made on
// its own output.
func (e *forexSettleEnv) ledgerPosting(t *testing.T, voucherID, ledgerID uuid.UUID) (string, string, bool) {
	t.Helper()
	var amount, drcr string
	found := false
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT p.amount::text, p.dr_cr::text
			  FROM posting p
			  JOIN voucher_head vh ON vh.current_version_id = p.voucher_version_id
			 WHERE vh.voucher_id = $1 AND p.ledger_id = $2`, voucherID, ledgerID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			if err := rows.Scan(&amount, &drcr); err != nil {
				return err
			}
			found = true
		}
		return rows.Err()
	}); err != nil {
		t.Fatalf("read posting: %v", err)
	}
	return amount, drcr, found
}

// billPending is what the bill has left, in BASE and in FOREIGN, netted the way
// Outstandings nets it. It is the free oracle for every settlement below: a
// realised figure that is right and a book value that was cleared wrong leaves a
// residue here, and nothing else in this file would see it.
func (e *forexSettleEnv) billPending(t *testing.T, ledgerID uuid.UUID, ref string) (string, string) {
	t.Helper()
	var base, foreign string
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT COALESCE(SUM(CASE WHEN b.dr_cr='D' THEN b.amount ELSE -b.amount END),0)::numeric(19,4)::text,
			       COALESCE(SUM(CASE WHEN b.dr_cr='D' THEN b.foreign_amount ELSE -b.foreign_amount END),0)::numeric(19,4)::text
			  FROM bill_allocation b
			  JOIN voucher_head vh ON vh.current_version_id = b.voucher_version_id
			 WHERE b.company_id = $1 AND b.ledger_id = $2 AND b.ref_name = $3
			   AND NOT vh.is_deleted`, e.company, ledgerID, ref).Scan(&base, &foreign)
	}); err != nil {
		t.Fatalf("read bill pending: %v", err)
	}
	return base, foreign
}

func (e *forexSettleEnv) mustPost(t *testing.T, in VoucherInput) uuid.UUID {
	t.Helper()
	res, err := e.led.PostVoucher(e.ctx, in)
	if err != nil {
		t.Fatalf("post %s: %v", in.VoucherNo, err)
	}
	return res.VoucherID
}

// assertRealised names the amount AND the ledger AND the side, separately.
func (e *forexSettleEnv) assertRealised(t *testing.T, voucherID uuid.UUID, wantAmount, wantSide, where string) {
	t.Helper()
	got, side, found := e.ledgerPosting(t, voucherID, e.forexPL)
	if !found {
		t.Errorf("%s: NO POSTING REACHED THE FOREX GAIN/LOSS LEDGER at all, want %s %s — "+
			"the difference the business realised is in no ledger, and the voucher "+
			"balanced without it", where, wantSide, wantAmount)
		return
	}
	if got != wantAmount {
		t.Errorf("%s: THE REALISED AMOUNT on the Forex Gain/Loss ledger is %s, want %s",
			where, got, wantAmount)
	}
	if side != wantSide {
		t.Errorf("%s: THE REALISED DIFFERENCE went to the %s side, want %s — a gain "+
			"booked as a loss is wrong by TWICE the figure and the voucher still "+
			"balances", where, side, wantSide)
	}
}

// ---------------------------------------------------------------------------
// 1. THE REFUSAL, AND IT IS BUILT FIRST BECAUSE IT IS THE WHOLE POINT
// ---------------------------------------------------------------------------

// TestSettlementThatRealisesADifferenceIsRefusedWhenNoLedgerIsChosen is the
// assertion this step exists for. ⛔ THE ALTERNATIVE IS NOT AN ERROR MESSAGE, IT IS
// A SILENT WRONG NUMBER: with no ledger chosen and no refusal, the 1,600 rupees has
// to go somewhere, and every available "somewhere" is a lie — absorbed into the
// party balance it makes a settled bill show a permanent residue; absorbed into the
// bank leg it misstates the cash actually received; guessed into a plausibly-named
// ledger it puts an operator's P&L into an account they never chose.
func TestSettlementThatRealisesADifferenceIsRefusedWhenNoLedgerIsChosen(t *testing.T) {
	e := setupForexSettle(t)
	e.mustPost(t, e.foreignSale("EXP-1"))

	// ⚠️ NO chooseForexLedger CALL. The company_forex_settings row does not even
	// exist, which is the state every company in the field is in today — "no row"
	// and "a row with a NULL choice" must both refuse, and the second case is
	// asserted below.
	_, err := e.led.PostVoucher(e.ctx,
		e.receipt("RCT-1", "EXP-1", "1000.00", settleRate, settleBillBase, "1600.0046", true))
	if err == nil {
		t.Fatalf("A SETTLEMENT REALISING 1600.0046 WAS ACCEPTED with no forex " +
			"gain/loss ledger chosen. The difference was posted to a ledger nobody " +
			"selected, or absorbed, and the operator was told nothing.")
	}
	if !strings.Contains(err.Error(), "forex_gain_loss_ledger_id") {
		t.Errorf("the refusal is %q — it must NAME THE SETTING the operator has to "+
			"choose (forex_gain_loss_ledger_id), because a refusal an operator "+
			"cannot act on is an outage", err.Error())
	}
	if !strings.Contains(err.Error(), "1600.0046") {
		t.Errorf("the refusal is %q — it must name the AMOUNT that could not be "+
			"posted (1600.0046), so the operator can see the difference is real "+
			"and material rather than a rounding artefact", err.Error())
	}
}

// TestARowWithANullChoiceRefusesExactlyAsNoRowDoes — 0080's own comment says the
// row may exist to hold a future setting while no ledger has been chosen, so
// "not chosen" has TWO encodings and both must refuse. A read that treated a
// missing row as unset and a NULL column as chosen-but-zero would accept this.
func TestARowWithANullChoiceRefusesExactlyAsNoRowDoes(t *testing.T) {
	e := setupForexSettle(t)
	e.mustPost(t, e.foreignSale("EXP-1"))
	e.chooseForexLedger(t, uuid.Nil) // the row exists; the choice is NULL

	_, err := e.led.PostVoucher(e.ctx,
		e.receipt("RCT-1", "EXP-1", "1000.00", settleRate, settleBillBase, "1600.0046", true))
	if err == nil {
		t.Fatalf("a company_forex_settings row with a NULL choice ACCEPTED a " +
			"settlement realising 1600.0046")
	}
	if !strings.Contains(err.Error(), "forex_gain_loss_ledger_id") {
		t.Errorf("the refusal is %q, want it to name forex_gain_loss_ledger_id", err.Error())
	}
}

// ---------------------------------------------------------------------------
// 2. THE ARITHMETIC, IN BOTH DIRECTIONS
// ---------------------------------------------------------------------------

// TestRealisedGainOnAForeignReceivableCreditsTheChosenLedger — $1,000 booked at
// 83.6512345678 (base 83,651.23) and received at 85.2512345678.
//
//	rupees actually received = 1000 × 85.2512345678 = 85,251.2345678 -> 85,251.2346
//	book value cleared       =                                          83,651.23
//	realised GAIN            =                                           1,600.0046
//
// ⭐ AND THE BILL MUST REACH EXACTLY ZERO, in base AND in foreign. That is the
// assertion a formula which differenced the RATES instead of the VALUES fails: it
// derives 1,598.7654 and leaves the receivable holding the bill's rounding residue
// for ever.
func TestRealisedGainOnAForeignReceivableCreditsTheChosenLedger(t *testing.T) {
	e := setupForexSettle(t)
	e.chooseForexLedger(t, e.forexPL)
	e.mustPost(t, e.foreignSale("EXP-1"))

	vid := e.mustPost(t,
		e.receipt("RCT-1", "EXP-1", "1000.00", settleRate, settleBillBase, "1600.0046", true))

	e.assertRealised(t, vid, "1600.0046", "C", "full receipt of a $1,000 export bill")

	if bank, side, _ := e.ledgerPosting(t, vid, e.bank); bank != "85251.2346" || side != "D" {
		t.Errorf("THE BANK LEG is %s %s, want D 85251.2346 — the rupees actually "+
			"received are foreign × the settlement rate, and a bank figure that is "+
			"not that ties to no statement", side, bank)
	}
	base, foreign := e.billPending(t, e.debtor, "EXP-1")
	if base != "0.0000" {
		t.Errorf("THE BILL'S BASE PENDING is %s, want 0.0000 — a fully settled bill "+
			"that still shows a residue is what a rate-difference formula leaves "+
			"behind, and it ages for ever in Outstandings", base)
	}
	if foreign != "0.0000" {
		t.Errorf("THE BILL'S FOREIGN PENDING is %s, want 0.0000", foreign)
	}
}

// TestRealisedLossOnAForeignPayableDebitsTheChosenLedger is the SIGN CONTROL, and
// it is not a mirror-image formality: the same rise in the rate that GAINS on a
// receivable LOSES on a payable, and both settlements carry an identical
// `foreign × Δ`. An engine that read the difference off the rates alone, with no
// regard for which side of the books the bill sits on, posts this to the credit
// side — wrong by twice 1,600 — and the voucher still balances.
func TestRealisedLossOnAForeignPayableDebitsTheChosenLedger(t *testing.T) {
	e := setupForexSettle(t)
	e.chooseForexLedger(t, e.forexPL)
	e.mustPost(t, e.foreignPurchaseBill("IMP-1"))

	vid := e.mustPost(t,
		e.payment("PAY-1", "IMP-1", "1000.00", settleRate, settleBillBase, "1600.0046", false))

	e.assertRealised(t, vid, "1600.0046", "D", "full payment of a $1,000 import bill")

	if bank, side, _ := e.ledgerPosting(t, vid, e.bank); bank != "85251.2346" || side != "C" {
		t.Errorf("THE BANK LEG is %s %s, want C 85251.2346", side, bank)
	}
	if base, _ := e.billPending(t, e.creditor, "IMP-1"); base != "0.0000" {
		t.Errorf("THE PAYABLE'S BASE PENDING is %s, want 0.0000", base)
	}
}

// TestAFallingRateReversesBothSigns — the second settlement rate (79.1234567890),
// below the bill's, so the receivable LOSES and the payable GAINS. ⛔ THIS IS THE
// PROBE THAT KILLS A HARD-CODED CONSTANT: every figure here differs from every
// figure above.
//
//	rupees received = 1000 × 79.1234567890 = 79,123.4567890 -> 79,123.4568
//	book cleared    =                                          83,651.23
//	realised LOSS   =                                           4,527.7732
func TestAFallingRateReversesBothSigns(t *testing.T) {
	e := setupForexSettle(t)
	e.chooseForexLedger(t, e.forexPL)
	e.mustPost(t, e.foreignSale("EXP-1"))

	vid := e.mustPost(t,
		e.receipt("RCT-1", "EXP-1", "1000.00", settleRateLow, settleBillBase, "4527.7732", false))
	e.assertRealised(t, vid, "4527.7732", "D", "receivable settled at a FALLEN rate")

	if bank, side, _ := e.ledgerPosting(t, vid, e.bank); bank != "79123.4568" || side != "D" {
		t.Errorf("THE BANK LEG is %s %s, want D 79123.4568", side, bank)
	}
}

// ---------------------------------------------------------------------------
// 3. §6.2 — A PARTIAL SETTLEMENT IS A DIFFERENT CASE FROM A FULL ONE
// ---------------------------------------------------------------------------

// TestPartialSettlementRealisesOnlyTheSettledPortion. Settle $400 of the $1,000:
//
//	rupees received  = 400 × 85.2512345678 = 34,100.49382712 -> 34,100.4938
//	book cleared     = 83,651.23 × 400/1000 = 33,460.492      -> 33,460.4920
//	realised GAIN    =                                              640.0018
//
// ⛔ A PROBE THAT ONLY EVER SETTLES IN FULL CANNOT TELL `foreign_settled` FROM
// `foreign_billed`, and an engine that used the latter derives 1,600.0046 here — a
// figure 2.5× too large that still balances against a bank leg the client chose.
// ⭐ AND THE REMAINDER MUST STAY OPEN AT $600 / 50,190.74, which is the assertion
// that pins the PRO-RATA of the book value rather than the whole of it.
func TestPartialSettlementRealisesOnlyTheSettledPortion(t *testing.T) {
	e := setupForexSettle(t)
	e.chooseForexLedger(t, e.forexPL)
	e.mustPost(t, e.foreignSale("EXP-1"))

	vid := e.mustPost(t,
		e.receipt("RCT-1", "EXP-1", "400.00", settleRate, "33460.4920", "640.0018", true))
	e.assertRealised(t, vid, "640.0018", "C", "partial receipt of $400 against a $1,000 bill")

	base, foreign := e.billPending(t, e.debtor, "EXP-1")
	if base != "50190.7380" {
		t.Errorf("THE REMAINDER'S BASE PENDING is %s, want 50190.7380 — the bill's "+
			"book value less the pro-rata slice this receipt cleared", base)
	}
	if foreign != "600.0000" {
		t.Errorf("THE REMAINDER'S FOREIGN PENDING is %s, want 600.0000", foreign)
	}
}

// TestTwoPartialSettlementsCloseTheBillExactly is the COMPOSITION invariant, and it
// is the one that decides whether the last settlement clears the PRO-RATA share or
// the REMAINDER.
//
// $400 then $600. The pro-rata slices are 33,460.4920 and 50,190.7380, which happen
// to sum to the bill exactly — so the sibling test below uses figures where they do
// NOT, and that is the case the remainder branch exists for.
//
// ⭐ THE INVARIANT IS NOT "the two realised figures sum to the one-shot figure".
// They do not, and must not: two receipts genuinely receive 34,100.4938 and
// 51,150.7407 rupees, whose sum is a paisa short of the 85,251.2346 one receipt
// would have received. The invariant is `Σ realised == Σ rupees received − the
// bill's book value`, which is the DEFINITION of a realised difference and holds
// exactly however the settlement is sliced.
func TestTwoPartialSettlementsCloseTheBillExactly(t *testing.T) {
	e := setupForexSettle(t)
	e.chooseForexLedger(t, e.forexPL)
	e.mustPost(t, e.foreignSale("EXP-1"))

	v1 := e.mustPost(t, e.receipt("RCT-1", "EXP-1", "400.00", settleRate, "33460.4920", "640.0018", true))
	// 600 × 85.2512345678 = 51,150.74074068 -> 51,150.7407
	// book cleared: the REMAINDER, 83,651.23 − 33,460.4920 = 50,190.7380
	// realised: 51,150.7407 − 50,190.7380 = 960.0027
	v2 := e.mustPost(t, e.receipt("RCT-2", "EXP-1", "600.00", settleRate, "50190.7380", "960.0027", true))

	e.assertRealised(t, v1, "640.0018", "C", "first slice, $400")
	e.assertRealised(t, v2, "960.0027", "C", "second slice, $600")

	base, foreign := e.billPending(t, e.debtor, "EXP-1")
	if base != "0.0000" || foreign != "0.0000" {
		t.Errorf("AFTER TWO SLICES the bill still shows base %s / foreign %s, want "+
			"0.0000 / 0.0000 — a bill settled in parts that never reaches zero ages "+
			"for ever and no total on any report contradicts it", base, foreign)
	}
	// Σ realised == Σ received − book value: (34100.4938 + 51150.7407) − 83651.23
	got := dec("640.0018").Add(dec("960.0027"))
	want := dec("34100.4938").Add(dec("51150.7407")).Sub(dec(settleBillBase))
	if !got.Equal(want) {
		t.Errorf("Σ realised is %s, want %s — the definition of a realised "+
			"difference does not survive being sliced", got, want)
	}
}

// TestTheFinalSettlementClearsTheRemainderNotThePraRataShare forces the rounding
// drift the branch exists for: a $3 bill carried at 100.00 rupees, settled in three
// $1 slices. The pro-rata share is 100.00 × 1/3 = 33.3333 (recurring, cut at four
// places), so three pro-rata slices clear 99.9999 and leave ONE PAISA-HUNDREDTH
// open for ever. The final slice must clear 33.3334.
//
// ⛔ THAT RESIDUE IS THE DEFECT THIS BRANCH PREVENTS AND IT IS INVISIBLE: the bill
// is settled, the party is square, the P&L is right to the paisa, and Outstandings
// shows a 0.0001 debtor that nothing will ever collect and no operator can clear.
func TestTheFinalSettlementClearsTheRemainderNotTheProRataShare(t *testing.T) {
	e := setupForexSettle(t)
	e.chooseForexLedger(t, e.forexPL)

	// A $3 bill at exactly 33.3333333333, base stated as 100.00.
	in := e.foreignSale("EXP-THIRDS")
	in.Postings[0].Amount = dec("100.00")
	in.Postings[1].Amount = dec("100.00")
	in.Postings[1].ForeignAmount = dec("3.00")
	in.Postings[1].Rate = dec("33.3333333333")
	in.BillAllocations[0].Amount = dec("100.00")
	in.BillAllocations[0].ForeignAmount = dec("3.00")
	in.BillAllocations[0].Rate = dec("33.3333333333")
	e.mustPost(t, in)

	// Settled at 40.0000000000 flat, so every slice receives 40.00 rupees.
	//   slice 1 (not final): cleared 100.00 × 1/3 = 33.3333, realised 40.00 − 33.3333 =  6.6667
	//   slice 2 (not final): cleared                33.3333, realised                    6.6667
	//   slice 3 (FINAL):     cleared 100.00 − 66.6666 = 33.3334, realised 40.00 − 33.3334 = 6.6666
	e.mustPost(t, e.receipt("RCT-1", "EXP-THIRDS", "1.00", "40.0000000000", "33.3333", "6.6667", true))
	e.mustPost(t, e.receipt("RCT-2", "EXP-THIRDS", "1.00", "40.0000000000", "33.3333", "6.6667", true))
	v3 := e.mustPost(t, e.receipt("RCT-3", "EXP-THIRDS", "1.00", "40.0000000000", "33.3334", "6.6666", true))

	e.assertRealised(t, v3, "6.6666", "C", "the FINAL of three $1 slices")

	base, foreign := e.billPending(t, e.debtor, "EXP-THIRDS")
	if base != "0.0000" {
		t.Errorf("THE BILL'S BASE PENDING is %s, want 0.0000 — three pro-rata slices "+
			"of 33.3333 clear 99.9999 and strand a residue no operator can clear", base)
	}
	if foreign != "0.0000" {
		t.Errorf("THE BILL'S FOREIGN PENDING is %s, want 0.0000", foreign)
	}
}

// ---------------------------------------------------------------------------
// 4. THE VERIFY DESIGN — the engine refuses a figure it did not derive
// ---------------------------------------------------------------------------

// TestARealisedFigureTheEngineDidNotDeriveIsRefusedNamingBothNumbers. This is the
// decision §4.1 left open, made and pinned: the server DERIVES and VERIFIES, exactly
// as deriveAndVerifyGST and the TDS block in post() do, and never injects a leg.
//
// ⚠️ THE FIGURE UNDER TEST IS THE PLAUSIBLE WRONG ONE, NOT A SILLY ONE. 1,598.7654
// is what the memo's own `foreign × (r_settle − r_bill)` yields, and it is the
// number a careful implementer arrives at independently. A test that sent 1.00 would
// pass against an engine that only checked the sign.
func TestARealisedFigureTheEngineDidNotDeriveIsRefusedNamingBothNumbers(t *testing.T) {
	e := setupForexSettle(t)
	e.chooseForexLedger(t, e.forexPL)
	e.mustPost(t, e.foreignSale("EXP-1"))

	_, err := e.led.PostVoucher(e.ctx,
		e.receipt("RCT-1", "EXP-1", "1000.00", settleRate, settleBillBase, "1598.7654", true))
	if err == nil {
		t.Fatalf("A REALISED FIGURE OF 1598.7654 WAS ACCEPTED where the engine " +
			"derives 1600.0046. The P&L and the party ledger now disagree by 1.2392 " +
			"and the voucher balances, because the client absorbed the difference in " +
			"the bank leg.")
	}
	for _, want := range []string{"1598.7654", "1600.0046"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal is %q — it must name BOTH figures (%s), the way "+
				"deriveAndVerifyGST's does, because an operator cannot correct a "+
				"number the message does not state", err.Error(), want)
		}
	}
}

// TestARealisedFigureOnTheWrongSideIsRefused — the right magnitude, the wrong sign.
// ⛔ THE VOUCHER STILL BALANCES, because the client moved the bank leg to suit, so
// nothing but the derivation can catch it.
func TestARealisedFigureOnTheWrongSideIsRefused(t *testing.T) {
	e := setupForexSettle(t)
	e.chooseForexLedger(t, e.forexPL)
	e.mustPost(t, e.foreignSale("EXP-1"))

	in := e.receipt("RCT-1", "EXP-1", "1000.00", settleRate, settleBillBase, "1600.0046", true)
	// Flip the forex leg to DEBIT and rebalance the bank leg so the voucher is
	// still perfectly double-entry.
	in.Postings[2].DrCr = Debit
	in.Postings[0].Amount = dec(settleBillBase).Sub(dec("1600.0046"))

	if _, err := e.led.PostVoucher(e.ctx, in); err == nil {
		t.Fatalf("A GAIN POSTED AS A LOSS WAS ACCEPTED. The realised difference is " +
			"in the right ledger with the right magnitude on the wrong side, so the " +
			"P&L is wrong by 3200.0092 and the voucher balances.")
	}
}

// TestASettlementThatOmitsTheRealisedLegIsRefused — the client sends the two
// obvious legs and no third. ⛔ THIS IS THE SHAPE §4.1's "generate" design would
// have had to accept, and validatePostings refuses it before anything could be
// derived: `debit 85251.2346 != credit 83651.2300`. The refusal is the double-entry
// guard, which is the correct authority and the reason the verify design does not
// need to touch it.
func TestASettlementThatOmitsTheRealisedLegIsRefused(t *testing.T) {
	e := setupForexSettle(t)
	e.chooseForexLedger(t, e.forexPL)
	e.mustPost(t, e.foreignSale("EXP-1"))

	in := e.receipt("RCT-1", "EXP-1", "1000.00", settleRate, settleBillBase, "1600.0046", true)
	in.Postings = in.Postings[:2] // drop the forex leg, leave the bank leg alone

	_, err := e.led.PostVoucher(e.ctx, in)
	if err == nil {
		t.Fatalf("a settlement with NO realised leg and an unbalanced pair was accepted")
	}
	if !strings.Contains(err.Error(), "double-entry violation") {
		t.Errorf("the refusal is %q, want the double-entry guard — if some other "+
			"check caught this first, the guard has been bypassed for settlements",
			err.Error())
	}
}

// TestASettlementThatOmitsTheLegAndBalancesTheBankIsAlsoRefused closes the door the
// test above leaves open. Drop the forex leg AND pull the bank leg down to match, so
// the voucher balances perfectly and the double-entry guard has nothing to say. Only
// the derivation catches this, and it is the shape a client that knows nothing about
// step 5 actually sends.
func TestASettlementThatOmitsTheLegAndBalancesTheBankIsAlsoRefused(t *testing.T) {
	e := setupForexSettle(t)
	e.chooseForexLedger(t, e.forexPL)
	e.mustPost(t, e.foreignSale("EXP-1"))

	in := e.receipt("RCT-1", "EXP-1", "1000.00", settleRate, settleBillBase, "0", true)
	if len(in.Postings) != 2 {
		t.Fatalf("fixture: expected two legs with a zero realised figure, got %d", len(in.Postings))
	}
	_, err := e.led.PostVoucher(e.ctx, in)
	if err == nil {
		t.Fatalf("A SETTLEMENT WITH NO REALISED LEG AND A BALANCED BANK LEG WAS " +
			"ACCEPTED. The bank shows 83651.23 received where 85251.2346 arrived, " +
			"the bill closes, the voucher balances, and the 1600.0046 gain is in no " +
			"ledger — there is no trace of it anywhere in the books.")
	}
	if !strings.Contains(err.Error(), "1600.0046") {
		t.Errorf("the refusal is %q, want it to name the 1600.0046 the engine derived",
			err.Error())
	}
}

// ---------------------------------------------------------------------------
// 5. ⛔ §6.1's EIGHTH CANDIDATE — AN ALTER RE-STATES
// ---------------------------------------------------------------------------

// TestAnAlterThatOmitsTheRealisedLegIsRefused. The realised difference is a third
// leg on a voucher an operator can later open and edit, which is the exact shape
// that has cost this repository seven defects. §6.1 asks the question directly:
// what does an alter that omits it do?
//
// ⭐ THE ANSWER IS "IT IS REFUSED", AND THAT IS STRUCTURAL RATHER THAN LUCKY. The
// seven prior occurrences all destroyed a ROW IN A SIDE TABLE, where zero rows is a
// legal state and nothing sums it. This value is a POSTING, so omitting it either
// unbalances the voucher (the double-entry guard refuses it) or is accompanied by a
// bank leg the derivation contradicts (this test). ⚠️ The derivation runs in post(),
// which is the SHARED path — an alter goes through every check a create does — and
// this test is what pins that, because a derivation placed in PostVoucher instead
// would pass every other test in this file.
func TestAnAlterThatOmitsTheRealisedLegIsRefused(t *testing.T) {
	e := setupForexSettle(t)
	e.chooseForexLedger(t, e.forexPL)
	e.mustPost(t, e.foreignSale("EXP-1"))

	settle := e.receipt("RCT-1", "EXP-1", "1000.00", settleRate, settleBillBase, "1600.0046", true)
	vid := e.mustPost(t, settle)
	e.assertRealised(t, vid, "1600.0046", "C", "the original settlement")

	// The ordinary round trip: open the voucher, change the narration, save — with
	// a client that rebuilt the body without the forex leg and balanced the bank
	// leg to suit, which is precisely what postingsFromView did for the triple.
	altered := settle
	altered.Narration = "corrected narration"
	altered.Postings = []PostingInput{
		{LedgerID: e.bank, DrCr: Debit, Amount: dec(settleBillBase)},
		{
			LedgerID: e.debtor, DrCr: Credit, Amount: dec(settleBillBase),
			CurrencyID: e.usd, ForeignAmount: dec("1000.00"), Rate: dec(settleRate),
		},
	}
	if _, err := e.led.AlterVoucher(e.ctx, vid, altered); err == nil {
		t.Fatalf("AN ALTER SILENTLY DESTROYED THE REALISED GAIN. The 1600.0046 that " +
			"was in the P&L is gone, the bill still closes, the voucher still " +
			"balances, and the eighth occurrence of \"an alter re-states\" has " +
			"shipped.")
	}

	// ⭐ AND THE INVERSE CONTROL. Without this, "the alter was refused" is equally
	// consistent with ALTERING A SETTLEMENT BEING BROKEN OUTRIGHT — which would
	// pass the assertion above and fail every operator.
	good := settle
	good.Narration = "corrected narration"
	if _, err := e.led.AlterVoucher(e.ctx, vid, good); err != nil {
		t.Fatalf("an alter that DOES restate the realised leg was refused: %v — the "+
			"refusal above proves nothing if no alter can succeed", err)
	}
	e.assertRealised(t, vid, "1600.0046", "C", "after a faithful alter")
	if base, _ := e.billPending(t, e.debtor, "EXP-1"); base != "0.0000" {
		t.Errorf("after the alter the bill's base pending is %s, want 0.0000 — the "+
			"alter's own AGAINST row was double-counted against the prior version's",
			base)
	}
}

// ---------------------------------------------------------------------------
// 6. ZERO AND NOT-APPLICABLE ARE DIFFERENT FACTS
// ---------------------------------------------------------------------------

// TestASettlementAtTheSameRateRealisesNothingAndNeedsNoLedger. ⛔ §6.2's trap, head
// on: zero is what a broken computation also returns, so this test asserts the
// SECOND fact that distinguishes them — that no ledger is required. An engine that
// demanded the setting whenever a foreign AGAINST row appeared would refuse this;
// an engine that never derived anything would accept it. Only a working derivation
// does both.
//
// ⚠️ THE BILL HERE IS A CLEAN ONE — $1,000 at 80.0000000000, base 80,000.00 — and it
// has to be, because the file's main specimen realises 0.0046 EVEN AT ITS OWN RATE.
// That is not a flaw in this test, it is the arithmetic of §4.1: the specimen's
// stored base (83,651.23) is not 1000 × 83.6512345678 (83,651.2346), so settling it
// at the rate it was raised at still hands over 46 hundredths of a paisa more than
// the books carry. A genuinely zero realisation needs base == foreign × rate, and
// stating that here is the difference between a test that pins zero and a test that
// pins "the engine did not run".
func TestASettlementAtTheSameRateRealisesNothingAndNeedsNoLedger(t *testing.T) {
	e := setupForexSettle(t)
	// ⚠️ NO chooseForexLedger, and that IS half the assertion.
	in := e.foreignSale("EXP-FLAT")
	in.Postings[0].Amount = dec("80000.00")
	in.Postings[1].Amount = dec("80000.00")
	in.Postings[1].Rate = dec("80.0000000000")
	in.BillAllocations[0].Amount = dec("80000.00")
	in.BillAllocations[0].Rate = dec("80.0000000000")
	e.mustPost(t, in)

	vid := e.mustPost(t,
		e.receipt("RCT-1", "EXP-FLAT", "1000.00", "80.0000000000", "80000.00", "0", true))

	if amount, side, found := e.ledgerPosting(t, vid, e.forexPL); found {
		t.Errorf("A FLAT-RATE SETTLEMENT REALISED %s %s. Nothing was gained or lost: "+
			"the rupees received are exactly the book value cleared", side, amount)
	}
	if base, foreign := e.billPending(t, e.debtor, "EXP-FLAT"); base != "0.0000" || foreign != "0.0000" {
		t.Errorf("the flat bill shows base %s / foreign %s pending, want 0.0000 / 0.0000",
			base, foreign)
	}
}

// TestAForeignAdvanceRealisesNothingRatherThanZero — an ADVANCE and an ON_ACCOUNT
// row have NO bill rate to difference against, so they realise nothing until they
// are allocated. ⛔ THE MEMO IS EXPLICIT THAT THIS IS NOT A ZERO: "state this rather
// than computing zero". The observable difference is that no ledger is required and
// no lookup is attempted — an engine that treated a missing opener as rate 0 would
// derive the entire rupee value of the advance as a realised gain.
func TestAForeignAdvanceRealisesNothingRatherThanZero(t *testing.T) {
	e := setupForexSettle(t)
	// ⚠️ NO chooseForexLedger, and that IS the assertion: a company with no choice
	// recorded must still be able to take a foreign advance.
	in := VoucherInput{
		Class:         Receipt,
		VoucherTypeID: e.receiptVT,
		PartyLedgerID: e.debtor,
		VoucherNo:     "ADV-1",
		VoucherDate:   "2025-09-15",
		Party:         "Globex LLC (USA)",
		CreatedBy:     "forex-settle-test",
		Postings: []PostingInput{
			{LedgerID: e.bank, DrCr: Debit, Amount: dec("85251.2346")},
			{
				LedgerID: e.debtor, DrCr: Credit, Amount: dec("85251.2346"),
				CurrencyID: e.usd, ForeignAmount: dec("1000.00"), Rate: dec(settleRate),
			},
		},
		BillAllocations: []BillAllocationInput{{
			LedgerID: e.debtor, RefName: "ADV-1", RefType: RefAdvance, DrCr: Credit,
			Amount:     dec("85251.2346"),
			CurrencyID: e.usd, ForeignAmount: dec("1000.00"), Rate: dec(settleRate),
		}},
	}
	vid := e.mustPost(t, in)
	if amount, side, found := e.ledgerPosting(t, vid, e.forexPL); found {
		t.Errorf("A FOREIGN ADVANCE REALISED %s %s to the Forex Gain/Loss ledger. It "+
			"differences against no bill, so there is nothing to realise; a figure "+
			"here means a missing opener was read as a zero rate", side, amount)
	}
}

// ---------------------------------------------------------------------------
// 7. §4.2's UNION — AN OPENING BILL IS A BILL
// ---------------------------------------------------------------------------

// TestAForeignOpeningBillCanBeSettledAndRealisesAgainstItsOwnRate. ⛔ THIS IS THE
// TEST THAT FAILS IF THE LOOKUP READS `bill_allocation` ALONE, and 0080 put the
// three columns on ledger_opening_bill precisely so it would not have to.
//
// A bill carried in on the ledger master has no voucher and therefore no NEW
// allocation row. Look for its rate in bill_allocation only and the engine finds no
// opener — at which point it either refuses a perfectly ordinary settlement (an
// outage on every migrated foreign debtor) or, worse, treats "no opener" as
// "nothing to realise" and silently drops the difference.
func TestAForeignOpeningBillCanBeSettledAndRealisesAgainstItsOwnRate(t *testing.T) {
	e := setupForexSettle(t)
	e.chooseForexLedger(t, e.forexPL)

	// $1,000 carried in at 83.6512345678, stated as 83,651.23 — the same specimen,
	// arriving by the other door.
	if err := exec(e.ctx, e.pool,
		`INSERT INTO ledger_opening_bill
		   (id, company_id, ledger_id, ref_name, ref_type, dr_cr, amount, due_date,
		    currency_id, foreign_amount, rate)
		 VALUES ($1, $2, $3, 'OPEN-1', 'NEW', 'D', $4, '2025-07-10', $5, $6, $7)`,
		uuid.New(), e.company, e.debtor, dec(settleBillBase),
		e.usd, dec(settleBillForeign), dec(settleBillRate)); err != nil {
		t.Fatalf("insert foreign opening bill: %v", err)
	}

	vid := e.mustPost(t,
		e.receipt("RCT-1", "OPEN-1", "1000.00", settleRate, settleBillBase, "1600.0046", true))
	e.assertRealised(t, vid, "1600.0046", "C",
		"settlement of a foreign bill that was OPENED ON THE LEDGER MASTER")
}

// ---------------------------------------------------------------------------
// 8. DATA ERRORS THE ENGINE MUST NOT AVERAGE OVER
// ---------------------------------------------------------------------------

// TestSettlingAForeignBillInADifferentCurrencyIsRefused. A $1,000 bill settled by an
// allocation tagged EUR. ⛔ Δrate ACROSS TWO CURRENCIES IS NOT A NUMBER: 85.25
// rupees-per-euro minus 83.65 rupees-per-dollar is 1.60 of nothing. The engine
// refuses rather than computing it, and Q5's rule is the same rule — a ref_name
// under two currencies is a data error, not a sum.
func TestSettlingAForeignBillInADifferentCurrencyIsRefused(t *testing.T) {
	e := setupForexSettle(t)
	e.chooseForexLedger(t, e.forexPL)
	e.mustPost(t, e.foreignSale("EXP-1"))

	in := e.receipt("RCT-1", "EXP-1", "1000.00", settleRate, settleBillBase, "1600.0046", true)
	in.Postings[1].CurrencyID = e.eur
	in.BillAllocations[0].CurrencyID = e.eur

	_, err := e.led.PostVoucher(e.ctx, in)
	if err == nil {
		t.Fatalf("A EUR SETTLEMENT OF A USD BILL WAS ACCEPTED, and the realised " +
			"difference was computed by subtracting a rupees-per-euro rate from a " +
			"rupees-per-dollar one")
	}
	if !strings.Contains(err.Error(), "EUR") || !strings.Contains(err.Error(), "USD") {
		t.Errorf("the refusal is %q — it must name BOTH currency codes, because "+
			"which one is wrong is the operator's decision, not ours", err.Error())
	}
}

// TestAForeignSettlementOfABaseBillIsRefused — a rupee bill settled by a dollar
// allocation. The bill has no rate, so there is nothing to difference against, and
// treating the absent rate as zero would realise the entire rupee value of the
// settlement as a gain.
func TestAForeignSettlementOfABaseBillIsRefused(t *testing.T) {
	e := setupForexSettle(t)
	e.chooseForexLedger(t, e.forexPL)

	// An ordinary rupee sale, bill-referenced.
	base := dec(settleBillBase)
	e.mustPost(t, VoucherInput{
		Class: Sales, VoucherTypeID: e.salesVT, PartyLedgerID: e.debtor,
		VoucherNo: "INR-1", VoucherDate: "2025-06-10", Party: "Globex LLC (USA)",
		CreatedBy: "forex-settle-test",
		Postings: []PostingInput{
			{LedgerID: e.sales, DrCr: Credit, Amount: base},
			{LedgerID: e.debtor, DrCr: Debit, Amount: base},
		},
		BillAllocations: []BillAllocationInput{{
			LedgerID: e.debtor, RefName: "INR-1", RefType: RefNew, DrCr: Debit,
			Amount: base, DueDate: "2025-07-10",
		}},
	})

	_, err := e.led.PostVoucher(e.ctx,
		e.receipt("RCT-1", "INR-1", "1000.00", settleRate, settleBillBase, "1600.0046", true))
	if err == nil {
		t.Fatalf("A DOLLAR SETTLEMENT OF A RUPEE BILL WAS ACCEPTED")
	}
	if !strings.Contains(err.Error(), "INR-1") {
		t.Errorf("the refusal is %q, want it to name the bill (INR-1)", err.Error())
	}
}

// ---------------------------------------------------------------------------
// 9. ⛔ THE CONTROL THE HOUSE STANDARD REQUIRES
// ---------------------------------------------------------------------------

// TestAnOrdinaryRupeeSettlementIsUntouchedByAllOfThis. Every bill in every book that
// exists today is base currency, and the overwhelming majority of settlements always
// will be. ⛔ IF STEP 5 MOVED ANY OF THEM IT WOULD BE A DEFECT, NOT A RE-PIN — this
// is the free oracle `verify:demo` reads, expressed as a unit test so it fails in
// seconds rather than in a browser.
//
// The company here has NO forex ledger chosen and NO currency on any row, which is
// the state of every company in the field: a derivation that ran unconditionally, or
// that read the setting before deciding whether it needed it, refuses this voucher
// and takes bill-wise settlement down for every existing user.
func TestAnOrdinaryRupeeSettlementIsUntouchedByAllOfThis(t *testing.T) {
	e := setupForexSettle(t)
	base := dec("50000.00")

	e.mustPost(t, VoucherInput{
		Class: Sales, VoucherTypeID: e.salesVT, PartyLedgerID: e.debtor,
		VoucherNo: "INR-1", VoucherDate: "2025-06-10", Party: "Globex LLC (USA)",
		CreatedBy: "forex-settle-test",
		Postings: []PostingInput{
			{LedgerID: e.sales, DrCr: Credit, Amount: base},
			{LedgerID: e.debtor, DrCr: Debit, Amount: base},
		},
		BillAllocations: []BillAllocationInput{{
			LedgerID: e.debtor, RefName: "INR-1", RefType: RefNew, DrCr: Debit,
			Amount: base, DueDate: "2025-07-10",
		}},
	})

	// A partial rupee receipt against it — the ordinary case, no currency anywhere.
	vid := e.mustPost(t, VoucherInput{
		Class: Receipt, VoucherTypeID: e.receiptVT, PartyLedgerID: e.debtor,
		VoucherNo: "RCT-1", VoucherDate: "2025-09-15", Party: "Globex LLC (USA)",
		CreatedBy: "forex-settle-test",
		Postings: []PostingInput{
			{LedgerID: e.bank, DrCr: Debit, Amount: dec("20000.00")},
			{LedgerID: e.debtor, DrCr: Credit, Amount: dec("20000.00")},
		},
		BillAllocations: []BillAllocationInput{{
			LedgerID: e.debtor, RefName: "INR-1", RefType: RefAgainst, DrCr: Credit,
			Amount: dec("20000.00"),
		}},
	})

	if amount, side, found := e.ledgerPosting(t, vid, e.forexPL); found {
		t.Errorf("A RUPEE SETTLEMENT REACHED THE FOREX GAIN/LOSS LEDGER with %s %s. "+
			"Nothing about a base-currency receipt involves a rate", side, amount)
	}
	base2, foreign := e.billPending(t, e.debtor, "INR-1")
	if base2 != "30000.0000" {
		t.Errorf("THE RUPEE BILL'S PENDING is %s, want 30000.0000 — step 5 moved an "+
			"ordinary settlement", base2)
	}
	if foreign != "0.0000" {
		t.Errorf("a base bill reports foreign pending %s, want 0.0000", foreign)
	}
}
