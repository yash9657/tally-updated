package reporting_test

// balance_sheet_period_caption_test.go — THE BALANCE SHEET'S PROFIT LINE MUST
// NAME THE PERIOD IT ACTUALLY COVERS.
//
// ⛔ WHAT WAS DRIVEN. With F2's From set to 2026-07-01 a browser showed
// 1,16,237.15 on the Balance Sheet against -37,911.02 on the Profit & Loss, over
// one set of books, and the Balance Sheet's line was still captioned "(Current
// Period)". The FIGURES are both right and must not be touched: a Balance Sheet
// is a POSITION, its API takes `as_on` only, and its implicit P&L period runs
// from inception. The P&L is a statement FOR A PERIOD and honours both bounds.
// They differ by everything posted before the From date, and that is arithmetic,
// not a defect.
//
// The defect was the CAPTION — a line that told an accountant the two statements
// covered the same window when they never did, so a reconciliation between them
// had nothing to reconcile against.
//
// ⛔ AND THE CONTROL IS THE HALF THAT MATTERS MOST. "Fixing" this by making the
// figure honour a From date was TRIED and it BREAKS THE SHEET (see the block at
// the fold-in: assets 7500 against liabilities 500, because the profit side
// loses the pre-books stock while the asset side keeps it). So this file asserts
// BOTH that the caption changed AND that the NUMBER did not: the Balance Sheet's
// net profit must still equal an UNBOUNDED Profit & Loss to the same date, which
// is the identity internal/migration's oracle checks on every corpus.

import (
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/reporting"
)

// profitLineOn returns the Balance Sheet's computed profit node — found by the
// PREFIX every spelling of it shares, because the whole point of the fix is that
// the rest of the name states a period and therefore varies with the request.
func profitLineOn(t *testing.T, bs reporting.BalanceSheet) reporting.StatementNode {
	t.Helper()
	var hits []reporting.StatementNode
	for _, n := range bs.Liabilities.Nodes {
		if strings.HasPrefix(n.Name, "Profit & Loss A/c") {
			hits = append(hits, n)
		}
	}
	if len(hits) != 1 {
		t.Fatalf("the Balance Sheet carries %d profit lines, want exactly 1; Liabilities is:%s",
			len(hits), describeTree(bs.Liabilities.Nodes))
	}
	return hits[0]
}

// TestTheBalanceSheetsProfitLineNamesItsOwnPeriodAndNotTheCurrentOne is the
// caption half of D46.
func TestTheBalanceSheetsProfitLineNamesItsOwnPeriodAndNotTheCurrentOne(t *testing.T) {
	pool, ctx, companyID, ids := setup(t)
	l := seedBooks(t, pool, ctx, ids) // everything in April 2025
	rep := reporting.NewService(pool)
	postJournalOn(t, pool, ctx, ids, "2025-07-15", l.rent, l.cash, "777")

	bs, err := rep.BalanceSheet(ctx, companyID, "2025-12-31")
	if err != nil {
		t.Fatalf("balance sheet: %v", err)
	}
	line := profitLineOn(t, bs)

	// ⛔ THE WORDS THAT WERE FALSE.
	if strings.Contains(line.Name, "Current Period") {
		t.Errorf("the Balance Sheet's profit line is still captioned %q — it is cumulative from "+
			"inception and the Profit & Loss beside it honours a From date, so this caption "+
			"asserts a window the figure does not cover", line.Name)
	}
	// It names the ONE bound it actually has.
	if !strings.Contains(line.Name, "2025-12-31") {
		t.Errorf("the Balance Sheet's profit line %q does not name the as-on date it was drawn at; "+
			"a Balance Sheet asked for a date must state it somewhere on its face", line.Name)
	}
	// And it is the net profit, not some other number that happens to be nearby.
	if !line.Amount.Equal(bs.NetProfit) {
		t.Errorf("the profit line reads %s while bs.NetProfit is %s",
			line.Amount.StringFixed(4), bs.NetProfit.StringFixed(4))
	}
	if line.Kind != reporting.NodeComputed {
		t.Errorf("the profit line is a %s node, want %s", line.Kind, reporting.NodeComputed)
	}

	// An UNBOUNDED sheet has no date to name, and says so in the package's own
	// vocabulary rather than trailing off after "to".
	all, err := rep.BalanceSheet(ctx, companyID, "")
	if err != nil {
		t.Fatalf("unbounded balance sheet: %v", err)
	}
	unbounded := profitLineOn(t, all).Name
	if strings.Contains(unbounded, "Current Period") {
		t.Errorf("the unbounded Balance Sheet's profit line is still captioned %q", unbounded)
	}
	if !strings.Contains(unbounded, "latest date posted") {
		t.Errorf("the unbounded Balance Sheet's profit line reads %q — with no as-on date it must "+
			"still say what it covers, not leave the reader to assume today", unbounded)
	}
}

// TestTheTwoStatementsMayDIFFERAndTheSheetNoLongerDeniesIt is the class
// assertion, and it is written as a pair on purpose.
//
//	(a) With a From date set, the two net profits DIFFER — by exactly the
//	    postings before it — and that must stay true.
//	(b) With NO From date, they AGREE to the paisa. That is what proves the
//	    caption fix did not quietly become a figure change.
func TestTheTwoStatementsMayDIFFERAndTheSheetNoLongerDeniesIt(t *testing.T) {
	pool, ctx, companyID, ids := setup(t)
	l := seedBooks(t, pool, ctx, ids) // everything in April 2025
	rep := reporting.NewService(pool)
	postJournalOn(t, pool, ctx, ids, "2025-07-15", l.rent, l.cash, "777")

	const asOn = "2025-12-31"
	bs, err := rep.BalanceSheet(ctx, companyID, asOn)
	if err != nil {
		t.Fatalf("balance sheet: %v", err)
	}

	// (a) The P&L for July onward sees ONLY the 777 expense.
	bounded, err := rep.ProfitAndLoss(ctx, companyID, "2025-07-01", asOn)
	if err != nil {
		t.Fatalf("bounded p&l: %v", err)
	}
	if !bounded.NetProfit.Equal(decimal.NewFromInt(-777)) {
		t.Fatalf("the July-onward P&L is %s, want -777.0000 — the fixture, not the code, has moved",
			bounded.NetProfit.StringFixed(4))
	}
	if bs.NetProfit.Equal(bounded.NetProfit) {
		t.Errorf("the Balance Sheet's cumulative profit equals the July-onward P&L (%s) — one of the "+
			"two statements has stopped honouring the bound it claims",
			bs.NetProfit.StringFixed(4))
	}
	// The caption over that divergence no longer claims the other statement's
	// window. THIS is the operator-visible half: the numbers were always
	// allowed to differ; the sentence was not.
	if name := profitLineOn(t, bs).Name; strings.Contains(name, "Current Period") {
		t.Errorf("two statements differ by %s and the sheet still captions its line %q",
			bs.NetProfit.Sub(bounded.NetProfit).StringFixed(4), name)
	}

	// (b) ⛔ THE NUMBER DID NOT MOVE. Unbounded below, both statements cover
	// inception-to-asOn, and they must agree exactly — the identity the corpus
	// oracle checks. If a later reader "fixes" the figure to honour a From
	// date, this fails and the sheet stops balancing besides.
	unbounded, err := rep.ProfitAndLoss(ctx, companyID, "", asOn)
	if err != nil {
		t.Fatalf("unbounded p&l: %v", err)
	}
	if !bs.NetProfit.Equal(unbounded.NetProfit) {
		t.Errorf("Balance Sheet net profit %s vs an unbounded P&L to the same date %s — these two "+
			"cover the same period and must agree to the paisa",
			bs.NetProfit.StringFixed(4), unbounded.NetProfit.StringFixed(4))
	}
	// And the sheet still balances, which the fold-in's own comment warns is
	// necessary and nowhere near sufficient.
	if !bs.IsBalanced() {
		t.Errorf("assets %s against liabilities %s",
			bs.Assets.Total.StringFixed(4), bs.Liabilities.Total.StringFixed(4))
	}
}
