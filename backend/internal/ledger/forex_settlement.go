package ledger

// forex_settlement.go — T8 STEP 5: REALISED GAIN/LOSS ON SETTLEMENT.
//
// A foreign bill raised at one rate and settled at another hands the business more
// or fewer rupees than its books carry, and the difference is REALISED at the moment
// of settlement. Until this file existed the write path stored both rates (step 4)
// and did nothing with them: a $1,000 export invoice booked at 83.65 and received at
// 85.25 posted happily, the receivable cleared, and the 1,600 rupees the business
// actually gained appeared in no ledger at all. Nothing refused it, because a
// voucher whose client-chosen bank leg matches its client-chosen party leg balances
// perfectly.
//
// =============================================================================
// ⭐⭐ DECISION 1 — THE SERVER *VERIFIES*, IT DOES NOT *GENERATE*. THIS REPOSITORY
//    CONTAINS BOTH PATTERNS AND A READER CANNOT INFER WHICH RULE APPLIES, SO:
// =============================================================================
// The client states every posting, including the realised leg, and names the ledger
// via company_forex_settings. The server derives the figure independently and
// REFUSES any disagreement, naming both numbers. It never inserts a leg.
//
// Three measured reasons, in the order they decide it:
//
//  1. ⛔ validatePostings REFUSES UNBALANCED INPUT — "double-entry violation: debit
//     %s != credit %s" — before anything could be derived. A generate design must
//     therefore either skip that check for settlements (removing the guard from the
//     one voucher class that most needs it: settlements are where cash meets a
//     receivable) or run the derivation before validation, computing a gain from
//     postings nothing has validated yet.
//
//  2. ⭐ IT IS THE ESTABLISHED PATTERN IN THIS EXACT FUNCTION, TWICE. deriveAndVerifyGST
//     "cross-checks the posted tax lines against the derived split … This guarantees
//     the books match the engine", and the TDS block in post() says of itself "Like
//     GST this NEVER injects a posting: it refuses a voucher whose TDS leg
//     contradicts the engine." The item-invoice path DOES generate — and the reason
//     is stated there and does not apply here: "The client had no postings to get
//     wrong." A settlement's postings are client-stated (a bank leg and a party
//     leg), which puts it squarely in the GST/TDS family rather than the
//     item-invoice one.
//
//  3. ⭐ A WRONG NUMBER BECOMES A 422 NAMING BOTH FIGURES rather than a silently
//     inserted line. When the client's arithmetic and the engine's disagree, the
//     operator is the only party who can say which is right, and a generated leg
//     would take that decision away without telling anyone it had been taken.
//
// ⚠️ THE CONSEQUENCE FOR STEP 8, stated here because it is not obvious from the
// screen's side: a verify design needs the CLIENT to compute the realised figure, so
// the settlement form must know the bill's historical rate. The open-bills read that
// feeds an AGAINST picker therefore has to return it — see openBillForex below,
// which is the same lookup the engine uses and is exported for exactly that reason.
//
// =============================================================================
// ⭐⭐ DECISION 2 — THE FORMULA IS `VALUE RECEIVED − BOOK VALUE CLEARED`, WHICH IS A
//    REFINEMENT OF THE MEMO'S `foreign × (r_settle − r_bill)` AND NOT A REJECTION
//    OF IT. THE TWO AGREE EXCEPT WHERE THE MEMO'S REFUSES A CORRECT VOUCHER.
// =============================================================================
// docs/design/t8-multi-currency-model.md Q3 states:
//
//	realised = foreign_settled × (rate_settlement − rate_bill)
//
// ⛔ THAT EXPRESSION CANNOT BALANCE A VOUCHER AGAINST A REAL BANK AMOUNT, and the
// counter-example is step 4's OWN FIXTURE. A $1,000 bill at 83.6512345678 is carried
// at a stored base of 83,651.23 — deliberately NOT 1000 × 83.6512345678
// (= 83,651.2345678), because Q2 makes the base amount stored and authoritative and
// fixed-4 money cannot hold the product anyway. Settle it at 85.2512345678:
//
//	rupees the bank actually receives   1000 × 85.2512345678 = 85,251.2346
//	receivable that must clear to ZERO                          83,651.23
//	                                                          ------------
//	the difference the books require                             1,600.0046
//	the memo's formula, 1000 × (85.2512345678 − 83.6512345678)   1,600.0000
//
// The memo's figure is 46 hundredths of a paisa short, so a voucher carrying it
// either misstates the cash received (bank leg 85,251.2300, tying to no statement,
// and T4's bank reconciliation would never match it) or strands 0.0046 on the
// receivable for ever, ageing in Outstandings with nothing to clear it.
//
// ⭐ SO THE ENGINE DIFFERENCES THE VALUES, NOT THE RATES:
//
//	realised = round(foreign_settled × rate_settlement, 4) − base_cleared
//
// and it REDUCES TO THE MEMO'S FORMULA EXACTLY when the bill's stored base is the
// arithmetic product (base = foreign × r_bill), which is the case the memo had in
// mind. It differs only by the bill's own rounding residue — i.e. only where the
// memo's formula would refuse a voucher that is right.
//
// ⚠️ AND THE RATES ARE NEVER QUANTISED BEFORE THE MULTIPLICATION. §6.3: a rate is
// NUMERIC(19,10) and money is (19,4). Round the two rates to 4 dp first and the
// example above yields 1,599.9700 — plausible, in the right ledger, on the right
// side, and wrong for ever. Only the PRODUCT is rounded, once, to money.
//
// =============================================================================
// ⭐⭐ DECISION 3 — base_cleared IS PRO-RATA, EXCEPT THE FINAL SLICE CLEARS THE
//    REMAINDER. WITHOUT THE EXCEPTION, BILLS SETTLED IN PARTS NEVER CLOSE.
// =============================================================================
// A settlement of f of a bill (base B, foreign F) clears round(B × f / F, 4) of the
// book value — pro-rata, because the base and the foreign figures must run out
// together. ⛔ BUT PRO-RATA DOES NOT COMPOSE. A ₹100.00 / $3.00 bill settled in three
// $1 slices clears 33.3333 three times, i.e. 99.9999, and strands 0.0001 on the
// party for ever: the bill is settled, the operator is square, the P&L is right to
// the paisa, and Outstandings shows a residue nothing will ever collect and no
// screen can clear. So the slice that exhausts the FOREIGN amount clears whatever
// base REMAINS, and Σ base_cleared == B exactly, however the settlement is sliced.
//
// ⚠️ base_cleared IS DERIVED AND VERIFIED, NOT TAKEN FROM THE BODY. It is the other
// half of the figure: an engine that trusted the client's stated allocation would
// derive its realised amount FROM the number under test, and verify nothing at all.

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// ⚠️ `moneyScale` is stock.go's, reused rather than restated. Its comment is the
// reason this file can round a rate-scaled product to money and have the figure
// agree with what compliance and the NUMERIC(19,4) columns do: one scale, one home.

// ErrForexLedgerNotChosen is the refusal Q3 requires, and its text names the
// SETTING rather than describing the problem: a refusal an operator cannot act on is
// an outage. The amount is appended by the caller so the operator can see the
// difference is material rather than a rounding artefact.
//
// ⛔ THE THREE ALTERNATIVES TO REFUSING ARE ALL SILENT WRONG NUMBERS. Absorbed into
// the party balance, a fully settled bill shows a permanent residue; absorbed into
// the bank leg, the cash received is misstated and reconciliation breaks; guessed
// into a plausibly-named ledger, an operator's Profit & Loss acquires a line they
// never chose. 0080's own header records the same three and reaches the same answer.
const errForexLedgerNotChosen = "ledger: this settlement realises an exchange " +
	"difference of %s, and no forex gain/loss ledger has been chosen for this " +
	"company: set company_forex_settings.forex_gain_loss_ledger_id (Gateway > F11 > " +
	"Multi-currency) and post the difference to it explicitly"

// billForex is one open bill's forex position as the BOOKS hold it — the opener's
// three values plus what has already been settled against it. It is the answer to
// the lookup §4.1 calls "the query the whole step rests on", and nothing in this
// repository did it before this file.
type billForex struct {
	// The opener: a bill_allocation NEW row, or a ledger_opening_bill row.
	Base     decimal.Decimal
	Foreign  decimal.Decimal
	Rate     decimal.Decimal
	Currency uuid.UUID
	Code     string
	// Settled so far, EXCLUDING the voucher being posted (see openBillForex).
	BaseSettled    decimal.Decimal
	ForeignSettled decimal.Decimal
	// Base is true for a bill carried in the books' own currency, which has no rate
	// to difference against. ⛔ Distinct from a zero rate, which the all_or_none
	// CHECK makes impossible and which an engine that defaulted a missing lookup
	// would invent — realising the entire rupee value of the settlement as a gain.
	Base_ bool
}

// settlement is this voucher's own AGAINST rows for ONE bill, aggregated.
type settlement struct {
	ledgerID   uuid.UUID
	refName    string
	currencyID uuid.UUID
	code       string
	foreign    decimal.Decimal
	rate       decimal.Decimal
	baseStated decimal.Decimal
	// receivable is true when the settlement CREDITS the party (a receipt clearing a
	// debtor). It decides the SIGN, and it is read off the allocation rather than
	// off the voucher class because a Journal can settle either side.
	receivable bool
}

// deriveAndVerifyRealisedForex is the whole of step 5's enforcement. It runs inside
// post()'s transaction, after the bill references have been stamped and resolved and
// after in.Postings is final, so a refusal rolls back the same way every other
// refusal there does.
//
// ⚠️ IT RETURNS EARLY AND FREE FOR EVERY VOUCHER IN EVERY BOOK THAT EXISTS TODAY.
// Not one posting in the field carries a currency, so the first loop finds nothing
// and the function does no query at all. That ordering is deliberate: reading
// company_forex_settings before deciding whether the answer is needed would put a
// query on the hot path of every rupee voucher in the product, and — worse — would
// let a company with no choice recorded refuse ordinary bill-wise settlement.
func deriveAndVerifyRealisedForex(
	ctx context.Context, tx pgx.Tx, companyID, voucherID uuid.UUID,
	postings []PostingInput, allocations []BillAllocationInput,
) error {
	settlements, err := aggregateForeignSettlements(allocations)
	if err != nil {
		return err
	}
	if len(settlements) == 0 {
		return nil
	}

	// The signed net difference, GAIN-POSITIVE: a positive figure is income and
	// belongs on the CREDIT side of the chosen ledger, a negative figure is an
	// expense and belongs on the DEBIT side.
	net := decimal.Zero
	for _, s := range settlements {
		// ⚠️ THE SETTLEMENT'S OWN CODE IS RESOLVED TOO, NOT ONLY THE BILL'S. The
		// mismatch refusal below has to name BOTH currencies, because which of the
		// two is the wrong one is the operator's decision and not ours — and a
		// message that named only the bill's ("this is a USD bill and this
		// settlement is in ") reads as a bug in the software rather than an error
		// in the entry.
		if s.code, err = currencyCode(ctx, tx, companyID, s.currencyID); err != nil {
			return err
		}
		bill, err := openBillForex(ctx, tx, companyID, voucherID, s.ledgerID, s.refName, allocations)
		if err != nil {
			return err
		}
		realised, err := realisedFor(s, bill)
		if err != nil {
			return err
		}
		net = net.Add(realised)
	}

	// ⭐ THE CHOSEN LEDGER IS READ ONLY NOW, AND ONLY IF THERE IS SOMETHING TO POST.
	// A settlement that realises nothing needs no ledger — which is the fact that
	// distinguishes "realised zero" from "the derivation never ran", and the reason
	// TestASettlementAtTheSameRateRealisesNothingAndNeedsNoLedger asserts BOTH.
	chosen, err := forexGainLossLedger(ctx, tx, companyID)
	if err != nil {
		return err
	}
	if chosen == uuid.Nil {
		if net.IsZero() {
			return nil
		}
		return fmt.Errorf(errForexLedgerNotChosen, net.Abs().StringFixed(moneyScale))
	}

	// Verified the way deriveAndVerifyGST verifies: sum what the caller POSTED to the
	// named ledger and refuse any disagreement with the engine. Signed, so ONE
	// comparison covers the amount and the side — a gain booked as a loss is wrong by
	// twice the figure and the voucher still balances, so the side is not a detail.
	posted := decimal.Zero
	for _, p := range postings {
		if p.LedgerID != chosen {
			continue
		}
		if p.DrCr == Credit {
			posted = posted.Add(p.Amount)
		} else {
			posted = posted.Sub(p.Amount)
		}
	}
	if !posted.Equal(net) {
		return fmt.Errorf("ledger: realised exchange difference: posting %s != derived %s "+
			"(signed, credit-positive; a gain is a CREDIT to the forex gain/loss ledger "+
			"and a loss is a DEBIT)",
			posted.StringFixed(moneyScale), net.StringFixed(moneyScale))
	}
	return nil
}

// aggregateForeignSettlements picks this voucher's AGAINST rows that carry a
// currency and groups them per bill.
//
// ⛔ ADVANCE AND ON_ACCOUNT ARE ABSENT BY CONSTRUCTION, NOT BY A ZERO. They
// difference against no bill, so they realise NOTHING — which is a different fact
// from realising zero, and the memo says so in those words. Filtering them here
// rather than computing zero for them is what makes the difference observable: a
// company with no forex ledger chosen can still take a foreign advance.
func aggregateForeignSettlements(allocations []BillAllocationInput) ([]settlement, error) {
	var out []settlement
	index := map[string]int{}
	for _, b := range allocations {
		if b.RefType != RefAgainst || b.forex().isBase() {
			continue
		}
		key := b.LedgerID.String() + "\x00" + b.RefName
		if i, ok := index[key]; ok {
			s := &out[i]
			// ⚠️ TWO AGAINST ROWS FOR ONE BILL AT TWO DIFFERENT RATES IS NOT AN
			// AVERAGE. One settlement happened; the rate is a fact about it, and
			// picking either row (or a weighted mean) invents a rate that no
			// document records.
			if !s.rate.Equal(b.Rate) || s.currencyID != b.CurrencyID {
				return nil, fmt.Errorf("ledger: bill %q is settled twice on this voucher "+
					"at two different rates (%s and %s) — one settlement has one rate, "+
					"and averaging them would invent a figure no document records",
					b.RefName, s.rate.String(), b.Rate.String())
			}
			s.foreign = s.foreign.Add(b.ForeignAmount)
			s.baseStated = s.baseStated.Add(b.Amount)
			continue
		}
		index[key] = len(out)
		out = append(out, settlement{
			ledgerID:   b.LedgerID,
			refName:    b.RefName,
			currencyID: b.CurrencyID,
			foreign:    b.ForeignAmount,
			rate:       b.Rate,
			baseStated: b.Amount,
			receivable: b.DrCr == Credit,
		})
	}
	return out, nil
}

// realisedFor is the arithmetic of Decisions 2 and 3, for one bill.
func realisedFor(s settlement, bill billForex) (decimal.Decimal, error) {
	if bill.Foreign.IsZero() && bill.Base_ {
		return decimal.Zero, fmt.Errorf("ledger: bill %q is carried in the books' own "+
			"currency and has no exchange rate, so a settlement of %s %s against it "+
			"differences against nothing — either the bill or the settlement is in the "+
			"wrong currency", s.refName, s.foreign.StringFixed(moneyScale), s.code)
	}
	if bill.Currency != s.currencyID {
		return decimal.Zero, fmt.Errorf("ledger: bill %q is a %s bill and this settlement "+
			"is in %s — a difference between a rate quoted per %s and one quoted per %s "+
			"is not a number", s.refName, bill.Code, s.code, bill.Code, s.code)
	}

	// Where the foreign amount runs out, the base must too.
	settledForeign := bill.ForeignSettled.Add(s.foreign)
	switch {
	case settledForeign.GreaterThan(bill.Foreign):
		return decimal.Zero, fmt.Errorf("ledger: bill %q is for %s %s and this settlement "+
			"would take the total settled to %s — an over-settled foreign bill has no "+
			"remaining book value to clear, so the realised difference is undefined",
			s.refName, bill.Code, bill.Foreign.StringFixed(moneyScale),
			settledForeign.StringFixed(moneyScale))
	case settledForeign.Equal(bill.Foreign):
		// THE FINAL SLICE: clear what remains, so the bill closes exactly.
		if want := bill.Base.Sub(bill.BaseSettled); !want.Equal(s.baseStated) {
			return decimal.Zero, baseClearedMismatch(s, want, "it is the FINAL settlement "+
				"of this bill, so it must clear the whole of the remaining book value or "+
				"the bill can never reach zero")
		}
	default:
		if want := bill.Base.Mul(s.foreign).Div(bill.Foreign).Round(moneyScale); !want.Equal(s.baseStated) {
			return decimal.Zero, baseClearedMismatch(s, want, "a partial settlement clears "+
				"the PRO-RATA share of the bill's book value, so that the base and the "+
				"foreign figures run out together")
		}
	}

	// ⛔ ONE ROUNDING, ON THE PRODUCT, AT MONEY SCALE. Not on the rate — see §6.3.
	received := s.foreign.Mul(s.rate).Round(moneyScale)
	gain := received.Sub(s.baseStated)
	if !s.receivable {
		// A PAYABLE: the rupees went OUT. Paying more than the books carry is a LOSS,
		// so the same rise in the rate that gains on a receivable loses here. An
		// engine that read the difference off the rates alone, with no regard for
		// which side of the books the bill sits on, is wrong by twice the figure and
		// the voucher still balances.
		gain = gain.Neg()
	}
	return gain, nil
}

func baseClearedMismatch(s settlement, want decimal.Decimal, why string) error {
	return fmt.Errorf("ledger: bill %q: this settlement clears %s of the bill's book "+
		"value, and the engine derives %s — %s. The realised difference is measured "+
		"against the book value cleared, so a wrong allocation makes a wrong gain",
		s.refName, s.baseStated.StringFixed(moneyScale), want.StringFixed(moneyScale), why)
}

// openBillForex is the lookup from an AGAINST reference back to the bill it settles.
//
// ⛔⛔ IT IS A UNION OF TWO TABLES AND THAT IS THE WHOLE POINT (§0.2 of the memo). A
// bill may have been opened by a voucher (bill_allocation, ref_type NEW) or CARRIED
// IN ON THE LEDGER MASTER (ledger_opening_bill) — and 0080 put the same three
// currency columns on the second table precisely so a foreign opening bill could be
// settled. Read bill_allocation alone and every migrated foreign debtor becomes
// unsettleable: the engine finds no opener and either refuses an ordinary receipt or,
// far worse, reads "no opener" as "nothing to realise" and drops the difference.
//
// ⛔ AND THIS VOUCHER IS EXCLUDED FROM BOTH SUMS. On an ALTER, voucher_head still
// points at the PREVIOUS version when this runs, so its AGAINST rows are live in the
// table and would be counted as prior settlements — the bill would look already
// settled and the alter would be refused for clearing too much. The exclusion is by
// voucher_id, so it covers every version of this logical voucher at once.
func openBillForex(
	ctx context.Context, tx pgx.Tx, companyID, voucherID, ledgerID uuid.UUID,
	refName string, thisVoucher []BillAllocationInput,
) (billForex, error) {
	var out billForex
	rows, err := tx.Query(ctx, `
		SELECT b.amount, b.currency_id, b.foreign_amount, b.rate
		  FROM bill_allocation b
		  JOIN voucher_head vh ON vh.current_version_id = b.voucher_version_id
		 WHERE b.company_id = $1 AND b.ledger_id = $2 AND b.ref_name = $3
		   AND b.ref_type = 'NEW' AND NOT vh.is_deleted AND vh.voucher_id <> $4
		UNION ALL
		SELECT ob.amount, ob.currency_id, ob.foreign_amount, ob.rate
		  FROM ledger_opening_bill ob
		 WHERE ob.company_id = $1 AND ob.ledger_id = $2 AND ob.ref_name = $3`,
		companyID, ledgerID, refName, voucherID)
	if err != nil {
		return out, fmt.Errorf("ledger: read the settled bill: %w", err)
	}
	found := 0
	for rows.Next() {
		var base decimal.Decimal
		var cur *uuid.UUID
		var foreign, rate *decimal.Decimal
		if err := rows.Scan(&base, &cur, &foreign, &rate); err != nil {
			rows.Close()
			return out, fmt.Errorf("ledger: read the settled bill: %w", err)
		}
		found++
		out.Base, out.Base_ = base, cur == nil
		if cur != nil {
			out.Currency, out.Foreign, out.Rate = *cur, *foreign, *rate
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("ledger: read the settled bill: %w", err)
	}
	if found > 1 {
		// ⚠️ Q5's rule, applied one layer earlier. validateDistinctBillOpeners already
		// refuses two voucher openers; this catches the pair it cannot see — a
		// ledger_opening_bill and a voucher NEW under one ref_name, which is a data
		// error and not a sum.
		return out, fmt.Errorf("ledger: bill %q is opened %d times on this party — by a "+
			"voucher and by the ledger's opening breakup — so which rate it was raised "+
			"at is ambiguous and the realised difference cannot be derived",
			refName, found)
	}
	if found == 0 {
		// The bill may be opened by THIS voucher (a Journal that raises and settles in
		// one document). Its rows are not in the table yet, so the input is the source.
		for _, b := range thisVoucher {
			if b.RefType == RefNew && b.LedgerID == ledgerID && b.RefName == refName {
				f := b.forex()
				out.Base, out.Base_ = b.Amount, f.isBase()
				out.Currency, out.Foreign, out.Rate = f.CurrencyID, f.ForeignAmount, f.Rate
				found = 1
				break
			}
		}
	}
	if found == 0 {
		return out, fmt.Errorf("ledger: this voucher settles bill %q, which these books "+
			"do not carry — a foreign settlement is measured against the rate the bill "+
			"was raised at, and there is no bill to read one from", refName)
	}

	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(b.amount), 0)::numeric(19,4),
		       COALESCE(SUM(b.foreign_amount), 0)::numeric(19,4)
		  FROM bill_allocation b
		  JOIN voucher_head vh ON vh.current_version_id = b.voucher_version_id
		 WHERE b.company_id = $1 AND b.ledger_id = $2 AND b.ref_name = $3
		   AND b.ref_type = 'AGAINST' AND NOT vh.is_deleted AND vh.voucher_id <> $4`,
		companyID, ledgerID, refName, voucherID,
	).Scan(&out.BaseSettled, &out.ForeignSettled); err != nil {
		return out, fmt.Errorf("ledger: read prior settlements of the bill: %w", err)
	}

	if !out.Base_ {
		if out.Code, err = currencyCode(ctx, tx, companyID, out.Currency); err != nil {
			return out, err
		}
	}
	return out, nil
}

// currencyCode resolves a currency id to the master's code, which is the only
// spelling of a currency an operator recognises. ⛔ Every refusal in this file names
// codes rather than uuids: "a USD bill settled in EUR" is a sentence an operator can
// act on, and a pair of uuids is a support ticket.
func currencyCode(ctx context.Context, tx pgx.Tx, companyID, id uuid.UUID) (string, error) {
	var code string
	if err := tx.QueryRow(ctx,
		`SELECT code FROM currency WHERE company_id = $1 AND id = $2`,
		companyID, id).Scan(&code); err != nil {
		return "", fmt.Errorf("ledger: read currency %s: %w", id, err)
	}
	return code, nil
}

// forexGainLossLedger reads the operator's choice, and returns uuid.Nil for BOTH
// encodings of "not chosen".
//
// ⛔ THERE ARE TWO, AND 0080'S OWN COMMENT SAYS SO: the row may not exist at all
// (every company in the field today), or it may exist with a NULL column because it
// was written to hold some other setting. A read that distinguished them would
// refuse one and accept the other, and the accepted one is the silent-wrong-number
// branch.
func forexGainLossLedger(ctx context.Context, tx pgx.Tx, companyID uuid.UUID) (uuid.UUID, error) {
	var id *uuid.UUID
	err := tx.QueryRow(ctx,
		`SELECT forex_gain_loss_ledger_id FROM company_forex_settings WHERE company_id = $1`,
		companyID).Scan(&id)
	switch {
	case err == pgx.ErrNoRows:
		return uuid.Nil, nil
	case err != nil:
		return uuid.Nil, fmt.Errorf("ledger: read company_forex_settings: %w", err)
	case id == nil:
		return uuid.Nil, nil
	}
	return *id, nil
}
