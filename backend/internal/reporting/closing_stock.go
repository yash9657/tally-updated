package reporting

// closing_stock.go — THE ONE DERIVATION OF CLOSING STOCK THE ACCOUNTS MAY USE.
//
// The Balance Sheet carries closing stock as a current asset; the Profit & Loss
// carries the Trading-account pair (opening stock on the debit side, closing
// stock on the credit side) so that
//
//	Gross Profit = Sales - (Opening Stock + Purchases - Closing Stock).
//
// Both statements must reach that figure through THE SAME code path the Stock
// Summary reaches it through, and this file is that path. Nothing here queries
// the movement ledger itself: it delegates to stockPositionsAsOn (the shared
// derivation in stock_summary_godown.go) and folds.
//
// WHY A SEPARATE FILE, AND WHY IT IS SO SMALL
// -------------------------------------------
// The value of stock is the single most order-dependent number in the product —
// a back-dated inward re-prices every later outward — so there must be exactly
// ONE place that says how a total closing value is obtained. Two screens that
// disagree about how much stock the company owns is worse than one screen that
// is wrong, because nobody can tell which to believe and both look plausible.
// StockSummary now folds through totalClosingValue too, so the agreement is
// structural rather than a convention two maintainers have to remember.
//
// THE ARITHMETIC IDENTITY THIS FILE SERVES, AND THE GATE THAT CANNOT SEE IT
// ------------------------------------------------------------------------
// Let A be assets, L liabilities+capital and NP = income - expense, all taken
// from loadClosingBalances. Double entry already gives A = L + NP. Folding a
// stock adjustment S into BOTH sides gives
//
//	Assets'      = A + S
//	Liabilities' = L + (NP + S)
//	Assets' - Liabilities' = (A + S) - (L + NP + S) = A - L - NP = 0.
//
// S CANCELS. The Balance Sheet therefore balances for ANY value of S, including
// a badly wrong one, as long as the same S lands on both sides. Which means
// IsBalanced() — and verify:demo's balance check, which the plan named as the
// gate for this feature — CANNOT catch a wrong closing-stock number. It catches
// exactly one bug class: an ASYMMETRIC fold-in (the asset without the profit
// term, or the two terms taken at different dates). Nothing more.
//
// That is why the load-bearing assertion for this feature is not "the sheet
// foots" but "the Balance Sheet's Closing Stock node equals
// StockSummary(asOn).TotalValue" — the same position rendered by a different
// report. Anyone weakening those tests should read this paragraph first.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// ErrUnboundedPreviousDay is returned by previousDay when handed an empty date.
//
// "" means UNBOUNDED everywhere on this seam, and the day before unbounded is
// not a date. Callers that legitimately have an unbounded lower bound must
// decide what that MEANS for them (openingStockValue decides: zero) rather than
// letting an empty string flow into a date function and come back as something
// that looks like an answer.
var ErrUnboundedPreviousDay = errors.New("reporting: the day before an unbounded date is not a date")

// totalClosingValue folds the closing value of every replayed position into one
// figure.
//
// StockSummary.TotalValue and the Balance Sheet's Stock-in-Hand line MUST be
// this same fold. Two screens that disagree about how much stock the company
// owns is worse than one screen that is wrong: an accountant reconciling the
// asset to the Stock Summary has no way to tell which of the two to believe,
// and both are printable.
//
// decimal.Add is EXACT — it is not floating point and there is no rounding step
// here — so the fold order is irrelevant and no sorting is needed for
// determinism of the total. (The rows are sorted, but for the benefit of the
// reader, not the arithmetic.) A float64 accumulator would make this total
// depend on the order the map iteration happened to produce, which is the kind
// of non-reproducibility that turns into "the Balance Sheet printed a different
// number this morning".
func totalClosingValue(rows []StockPositionRow) decimal.Decimal {
	total := decimal.Zero
	for _, r := range rows {
		total = total.Add(r.ClosingValue)
	}
	return total
}

// closingStockValue is the value of everything on hand as at `asOn`: the
// position the Stock Summary shows, folded to a single figure.
//
// WHY THIS TAKES A pgx.Tx AND IS NOT A *Service METHOD. BalanceSheet and
// ProfitAndLoss already hold a transaction inside their RunInTx closure, and
// db.Pool.RunInTx (internal/platform/db/db.go) calls pool.Begin
// UNCONDITIONALLY — there is no savepoint path and no "reuse the tx in ctx"
// path. Calling s.StockSummary(...) from inside the Balance Sheet closure would
// therefore do two damaging things at once:
//
//  1. It would hold one pooled connection while acquiring a second. At
//     pool-size concurrency that is a hard deadlock — every connection held by a
//     statement waiting for a connection — and it appears under load, in
//     production, not in a test.
//  2. It would read a SECOND SNAPSHOT. The accounting side of the sheet would
//     come from one point in time and the stock line from another, so a voucher
//     committed between the two would make the statement fail to balance. That
//     failure would be irreproducible by construction, which is the worst
//     property a wrong Balance Sheet can have.
//
// The outer RunInTx has already issued SET LOCAL app.current_company_id, so RLS
// and the books-beginning probe inside loadStockOpenings resolve correctly with
// no extra work here.
//
// `asOn` empty means "everything ever posted", exactly as everywhere else on
// this seam (StockSummary, loadClosingBalances, nullDate). That is precisely why
// openingStockValue must special-case an empty `from` instead of passing it
// through — see the comment there, it is the most important one in this file.
//
// ERRORS ARE PROPAGATED, INCLUDING THE UNVALUABLE-ITEM ONE. A stock item whose
// valuation_method has no implemented valuer makes replayPositions fail
// (inventory.ValuerFor returns an error rather than falling back to weighted
// average). That error aborts the WHOLE statement, including the accounting side
// that was perfectly fine. This is deliberate, and it is the same judgement
// ValuerFor itself makes: a Balance Sheet that cannot render is a support
// ticket; a Balance Sheet that renders a plausible wrong asset total is a tax
// notice. The arm is unreachable today (migration 0025's CHECK admits only
// WEIGHTED_AVG) and becomes reachable exactly in the deployment-skew case — a
// newer schema naming FIFO against an older binary — which is the case where a
// silently weighted-average "FIFO" sheet would do the most damage. No
// `continue`, no zero fallback, no "unvalued items" bucket.
func closingStockValue(ctx context.Context, tx pgx.Tx, asOn string) (decimal.Decimal, error) {
	rows, _, err := stockPositionsAsOn(ctx, tx, asOn)
	if err != nil {
		return decimal.Zero, err
	}
	return totalClosingValue(rows), nil
}

// previousDay is the calendar day before an ISO date: the date a P&L starting on
// `iso` must take its OPENING stock from.
//
// WHY THIS IS IN GO AND NOT `$1::date - 1` IN SQL. Postgres would compute it
// correctly, but it would put the rule behind a round trip where no unit test
// can reach it, and this rule is worth pinning directly: every interesting case
// is a boundary. 1 April (the Indian FY start, so the single most common input
// in this product) crosses a month; 1 January crosses a year; 1 March crosses
// February, whose length depends on the leap rule INCLUDING the century
// correction (1900 is not a leap year, 2000 is). A hand-rolled decrement of the
// last two characters gets every one of those wrong, and the first person to
// notice is whoever reads the March P&L and finds an opening stock figure that
// belongs to a different day.
//
// time.Parse with no zone information yields UTC midnight, and AddDate then
// moves whole calendar days, so no daylight-saving transition can turn a day
// into 23 hours and land the result on the same date it started from. (India
// has no DST, but the process locale is not ours to assume.)
//
// The output is always exactly 10 zero-padded characters, which is what keeps
// loadStockOpenings' lexical `asOn < begins` comparison correct across the
// seam — ISO dates only compare as strings while they stay zero-padded.
//
// It TrimSpaces first, matching nullDate's existing tolerance for a padded date
// off the wire, and it REFUSES an empty string: see ErrUnboundedPreviousDay.
func previousDay(iso string) (string, error) {
	s := strings.TrimSpace(iso)
	if s == "" {
		return "", ErrUnboundedPreviousDay
	}
	t, err := time.Parse(isoDate, s)
	if err != nil {
		return "", fmt.Errorf("reporting: %q is not a YYYY-MM-DD date: %w", iso, err)
	}
	return t.AddDate(0, 0, -1).Format(isoDate), nil
}

// openingStockValue is the value of everything on hand the instant BEFORE a P&L
// period opens — the position as at `from` minus one day.
//
// WHY THE OPENING TERM IS TAKEN AT from-1 AND NOT AT from. The P&L's period
// bounds are INCLUSIVE at both ends, so a voucher dated exactly on `from` is
// INSIDE the period and its stock must show up as a movement of the period, not
// as something the period started with. Taking the opening at `from` would
// double-count that day. It also breaks ADDITIVITY, which is a gate: for
// contiguous windows [d0,d1] and [d1+1,d2] the stock terms telescope,
//
//	S(d0,d1) + S(d1+1,d2) = (CS(d1)-CS(d0-1)) + (CS(d2)-CS(d1)) = CS(d2)-CS(d0-1),
//
// and that cancellation only happens because the second window's opening is read
// at (d1+1)-1 = d1, exactly the date the first window closed on. Read it at d1+1
// and the two halves stop sharing a boundary value, so they no longer sum to the
// whole; they differ by whatever moved on d1+1.
//
// THE EMPTY BRANCH — the most important three lines in this file.
// An empty `from` means the period is UNBOUNDED BELOW: there is no "before", so
// the opening stock is ZERO. Handing "" straight to closingStockValue would mean
// "everything ever posted" — the exact INVERSE of the intended zero. It would
// subtract the ENTIRE closing inventory as though the company had opened the
// period owning all of it, understating gross profit by the whole warehouse, and
// the P&L would still foot perfectly while doing it. This is not a hypothetical
// edge: reportDate in server.go returns "" for an absent `from` query param, so
// an unbounded P&L is the DEFAULT HTTP path.
//
// THE FIRST PERIOD, AND WHY ITS ZERO IS CORRECT RATHER THAN A LEAK. For the
// first period `from` == company.books_beginning_from, so previousDay(from) is
// strictly before the books began and loadStockOpenings returns nil — the
// opening stock comes out ZERO even though stock_opening rows exist. That is
// right: a stock_opening row is stated AS AT the books' beginning (it carries no
// date of its own), so it belongs INSIDE the first period, and its whole value
// flows through that period's closing stock into that period's profit. This is a
// PERIODIC inventory system and the schema records opening stock only in the
// inventory plane with no counter-credit in any ledger, so there is nowhere else
// for it to go.
//
// DO NOT "FIX" THIS by evaluating the books-beginning guard at `from` instead of
// at previousDay(from) — i.e. by copying StockMovementReport's deliberate `to`
// guard. That precedent does not apply here: StockMovementReport asks "was the
// window itself before the books began", this function asks "what did the
// company own the day before the window opened". Move the guard and the opening
// term becomes non-zero for the first period while the Balance Sheet's term
// (which correctly subtracts nothing — see below) does not, and the two
// statements disagree by exactly the full opening-stock value.
//
// The Balance Sheet uses the same general form with the period running from the
// books' beginning to `as_on`, so its opening term is CS(books_beginning - 1),
// which loadStockOpenings guarantees is zero. Its caller still writes the
// subtraction out in full rather than a bare Add, so that a later reader cannot
// "restore" a subtraction that is already there.
func openingStockValue(ctx context.Context, tx pgx.Tx, from string) (decimal.Decimal, error) {
	if strings.TrimSpace(from) == "" {
		return decimal.Zero, nil
	}
	prev, err := previousDay(from)
	if err != nil {
		return decimal.Zero, err
	}
	return closingStockValue(ctx, tx, prev)
}

