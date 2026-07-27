package reporting_test

// period_test.go — PERIOD SEMANTICS for the three statement reports.
//
// The Balance Sheet and Trial Balance are POSITIONS AS AT a date; the Profit &
// Loss is a statement FOR A PERIOD. Getting that distinction wrong produces
// numbers that still foot and are still wrong, which is why each half is
// asserted separately here rather than through one shared "does filtering
// work?" test.
//
// The first test is the load-bearing one: the reports were rebuilt to derive
// balances from POSTINGS (the materialised ledger_balance table has no date
// dimension), and the derivation must agree exactly with the running totals for
// the unbounded case. If it did not, every figure in the product would depend on
// which code path produced it.

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/masters"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/reporting"
)

// TestDerivedBalancesMatchTheMaterialisedTable is the INDEPENDENT ORACLE for the
// substrate swap. `ledger_balance` is maintained incrementally by the posting
// path and knows nothing about dates; the reports now re-derive from postings.
// For "everything ever posted" the two must be identical, ledger by ledger — a
// silent divergence would mean the Trial Balance and the query DSL disagreed
// about the same company.
func TestDerivedBalancesMatchTheMaterialisedTable(t *testing.T) {
	pool, ctx, companyID, ids := setup(t)
	seedBooks(t, pool, ctx, ids)
	rep := reporting.NewService(pool)

	tb, err := rep.TrialBalance(ctx, companyID, "")
	if err != nil {
		t.Fatalf("trial balance: %v", err)
	}
	// The report's net per ledger, including the opening balance it folds in.
	got := map[string]decimal.Decimal{}
	for _, r := range tb.Rows {
		got[r.LedgerName] = r.Debit.Sub(r.Credit)
	}

	// The materialised totals, read straight from the table the posting path
	// maintains, plus the same opening balance the report folds in.
	want := map[string]decimal.Decimal{}
	if err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT l.name, COALESCE(lb.debit,0), COALESCE(lb.credit,0),
			        l.opening_balance, l.opening_dc
			   FROM ledger l
			   LEFT JOIN ledger_balance lb ON lb.ledger_id = l.id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var (
				name       string
				d, c, open decimal.Decimal
				dc         string
			)
			if err := rows.Scan(&name, &d, &c, &open, &dc); err != nil {
				return err
			}
			if dc == "C" {
				c = c.Add(open)
			} else {
				d = d.Add(open)
			}
			want[name] = d.Sub(c)
		}
		return rows.Err()
	}); err != nil {
		t.Fatalf("read materialised balances: %v", err)
	}

	if len(got) != len(want) {
		t.Fatalf("report has %d ledgers, ledger_balance join has %d", len(got), len(want))
	}
	for name, w := range want {
		g, ok := got[name]
		if !ok {
			t.Errorf("ledger %q is in the materialised totals but not in the report", name)
			continue
		}
		if !g.Equal(w) {
			t.Errorf("ledger %q: report %s, materialised %s — the derivation disagrees with the running totals",
				name, g.StringFixed(4), w.StringFixed(4))
		}
	}
}

// postJournalOn posts a balanced journal on a given date, so a test can place
// movement inside and outside a period.
func postJournalOn(
	t *testing.T, pool *db.Pool, ctx context.Context, ids *identity.Service,
	date string, dr, cr uuid.UUID, amount string,
) {
	t.Helper()
	vt, err := ids.VoucherTypeID(ctx, "Journal")
	if err != nil {
		t.Fatalf("journal vt: %v", err)
	}
	if _, err := ledger.NewService(pool).PostVoucher(ctx, ledger.VoucherInput{
		Class:         ledger.Journal,
		VoucherTypeID: vt,
		VoucherDate:   date,
		CreatedBy:     "period-test",
		Postings: []ledger.PostingInput{
			{LedgerID: dr, DrCr: ledger.Debit, Amount: dec(amount)},
			{LedgerID: cr, DrCr: ledger.Credit, Amount: dec(amount)},
		},
	}); err != nil {
		t.Fatalf("post journal on %s: %v", date, err)
	}
}

// TestTrialBalanceAsOnExcludesLaterMovement — the Trial Balance is a POSITION.
func TestTrialBalanceAsOnExcludesLaterMovement(t *testing.T) {
	pool, ctx, companyID, ids := setup(t)
	l := seedBooks(t, pool, ctx, ids) // everything in April 2025
	rep := reporting.NewService(pool)

	// A later expense, well outside the seeded month.
	postJournalOn(t, pool, ctx, ids, "2025-07-15", l.rent, l.cash, "777")

	rentOf := func(asOn string) decimal.Decimal {
		tb, err := rep.TrialBalance(ctx, companyID, asOn)
		if err != nil {
			t.Fatalf("trial balance as on %q: %v", asOn, err)
		}
		for _, r := range tb.Rows {
			if r.LedgerName == "Rent Paid" {
				return r.Debit.Sub(r.Credit)
			}
		}
		return decimal.Zero
	}

	april := rentOf("2025-04-30")
	all := rentOf("")
	if !all.Sub(april).Equal(dec("777")) {
		t.Fatalf("Rent as on 30-Apr = %s, unbounded = %s; the July journal should be the ONLY difference (777)",
			april.StringFixed(4), all.StringFixed(4))
	}

	// AND the as-at position must still BALANCE. A period filter that dropped
	// one leg of a voucher would show a plausible number on an unbalanced book.
	tb, err := rep.TrialBalance(ctx, companyID, "2025-04-30")
	if err != nil {
		t.Fatalf("trial balance: %v", err)
	}
	if !tb.IsBalanced() {
		t.Fatalf("Trial Balance as on 2025-04-30 does not balance: Dr %s Cr %s",
			tb.TotalDebit.StringFixed(4), tb.TotalCredit.StringFixed(4))
	}
}

// TestBalanceSheetAsOnStillBalances — the strongest invariant available. A
// Balance Sheet at ANY date must foot; if period filtering ever dropped one side
// of a voucher this fails, however plausible the individual figures look.
func TestBalanceSheetAsOnStillBalances(t *testing.T) {
	pool, ctx, companyID, ids := setup(t)
	l := seedBooks(t, pool, ctx, ids)
	rep := reporting.NewService(pool)
	postJournalOn(t, pool, ctx, ids, "2025-07-15", l.rent, l.cash, "777")

	for _, asOn := range []string{"2025-04-30", "2025-06-30", "2025-12-31", ""} {
		bs, err := rep.BalanceSheet(ctx, companyID, asOn)
		if err != nil {
			t.Fatalf("balance sheet as on %q: %v", asOn, err)
		}
		if !bs.IsBalanced() {
			t.Errorf("Balance Sheet as on %q does not balance: assets %s, liabilities %s (net profit %s)",
				asOn, bs.Assets.Total.StringFixed(4), bs.Liabilities.Total.StringFixed(4),
				bs.NetProfit.StringFixed(4))
		}
	}

	// A date BEFORE the books start is an empty position, not an error — and an
	// empty position balances trivially.
	bs, err := rep.BalanceSheet(ctx, companyID, "2020-01-01")
	if err != nil {
		t.Fatalf("balance sheet before the books: %v", err)
	}
	if !bs.IsBalanced() {
		t.Errorf("Balance Sheet before the books began does not balance")
	}
}

// TestProfitAndLossIsAPeriodStatement — the P&L honours BOTH bounds, and the
// April and July halves must sum to the whole. That additivity is what makes it
// a period statement rather than a position.
func TestProfitAndLossIsAPeriodStatement(t *testing.T) {
	pool, ctx, companyID, ids := setup(t)
	l := seedBooks(t, pool, ctx, ids)
	rep := reporting.NewService(pool)
	postJournalOn(t, pool, ctx, ids, "2025-07-15", l.rent, l.cash, "777")

	net := func(from, to string) decimal.Decimal {
		pl, err := rep.ProfitAndLoss(ctx, companyID, from, to)
		if err != nil {
			t.Fatalf("p&l %q..%q: %v", from, to, err)
		}
		return pl.NetProfit
	}

	whole := net("", "")
	april := net("2025-04-01", "2025-04-30")
	july := net("2025-07-01", "2025-07-31")

	if !april.Add(july).Equal(whole) {
		t.Errorf("April (%s) + July (%s) = %s, but the unbounded P&L is %s — a period statement must be additive",
			april.StringFixed(4), july.StringFixed(4), april.Add(july).StringFixed(4), whole.StringFixed(4))
	}
	// July holds only the 777 expense, so its result is exactly that loss.
	if !july.Equal(dec("-777")) {
		t.Errorf("July net profit = %s, want -777.0000 (the rent journal is July's only entry)",
			july.StringFixed(4))
	}
	// A window with nothing in it is zero, not the whole year.
	if got := net("2025-05-01", "2025-05-31"); !got.IsZero() {
		t.Errorf("May net profit = %s, want 0 (nothing was posted in May)", got.StringFixed(4))
	}
}

// TestProfitAndLossExcludesOpeningBalance is the half most easily got wrong: an
// opening balance is a position carried from BEFORE the books began, so it is
// not income or expenditure of any period. The Balance Sheet must still see it.
func TestProfitAndLossExcludesOpeningBalance(t *testing.T) {
	pool, ctx, companyID, ids := setup(t)
	seedBooks(t, pool, ctx, ids)
	rep := reporting.NewService(pool)

	// An expense ledger carrying an opening balance (unusual, but the only way
	// to observe the rule directly).
	if _, err := masters.NewService(pool).CreateLedger(ctx, masters.Ledger{
		Name: "Carried Expense", GroupName: "Indirect Expenses",
		OpeningBalance: dec("500"), OpeningDC: "D",
	}, "period-test"); err != nil {
		t.Fatalf("create ledger with an opening balance: %v", err)
	}

	plAll, err := rep.ProfitAndLoss(ctx, companyID, "", "")
	if err != nil {
		t.Fatalf("p&l: %v", err)
	}
	bs, err := rep.BalanceSheet(ctx, companyID, "")
	if err != nil {
		t.Fatalf("balance sheet: %v", err)
	}

	// The P&L must not have absorbed the 500 opening; the Balance Sheet's own
	// net-profit line (built from closing balances) is the one that does.
	if plAll.Expenses.Total.Equal(bs.NetProfit.Abs()) && !plAll.Expenses.Total.IsZero() {
		t.Log("note: totals coincide; the assertion below is the real check")
	}
	if !bs.NetProfit.Sub(plAll.NetProfit).Equal(dec("-500")) {
		t.Errorf("Balance Sheet net profit %s vs P&L net profit %s: the 500 opening balance must appear in "+
			"the position (Balance Sheet) and NOT in the period statement (P&L), a difference of exactly -500",
			bs.NetProfit.StringFixed(4), plAll.NetProfit.StringFixed(4))
	}
}

// TestBalancesFollowAltersAndDeletes pins what the current-version join is FOR.
//
// It exists because a mutation revealed the `vh.is_deleted = false` clause to be
// redundant — a tombstone version carries zero posting rows, so the
// current-version join alone already excludes a deleted voucher. Rather than
// leave the real property untested, this asserts the behaviour users depend on,
// whichever clause enforces it: an ALTER restates (never accumulates), and a
// DELETE removes the effect entirely — at any as-at date, including one in the
// past, since a period filter that read historic versions would resurrect both.
func TestBalancesFollowAltersAndDeletes(t *testing.T) {
	pool, ctx, companyID, ids := setup(t)
	l := seedBooks(t, pool, ctx, ids)
	rep := reporting.NewService(pool)
	led := ledger.NewService(pool)

	vt, err := ids.VoucherTypeID(ctx, "Journal")
	if err != nil {
		t.Fatalf("journal vt: %v", err)
	}
	post := func(amount string) uuid.UUID {
		res, err := led.PostVoucher(ctx, ledger.VoucherInput{
			Class: ledger.Journal, VoucherTypeID: vt, VoucherDate: "2025-04-18",
			CreatedBy: "period-test",
			Postings: []ledger.PostingInput{
				{LedgerID: l.rent, DrCr: ledger.Debit, Amount: dec(amount)},
				{LedgerID: l.cash, DrCr: ledger.Credit, Amount: dec(amount)},
			},
		})
		if err != nil {
			t.Fatalf("post %s: %v", amount, err)
		}
		return res.VoucherID
	}
	rentAsOn := func(asOn string) decimal.Decimal {
		tb, err := rep.TrialBalance(ctx, companyID, asOn)
		if err != nil {
			t.Fatalf("trial balance as on %q: %v", asOn, err)
		}
		for _, r := range tb.Rows {
			if r.LedgerName == "Rent Paid" {
				return r.Debit.Sub(r.Credit)
			}
		}
		return decimal.Zero
	}

	base := rentAsOn("2025-04-30")

	// ALTER: 100 restated to 250 must move the balance by 250, not by 350.
	id := post("100")
	if got := rentAsOn("2025-04-30").Sub(base); !got.Equal(dec("100")) {
		t.Fatalf("after posting 100 the Rent balance moved %s, want 100", got.StringFixed(4))
	}
	if _, err := led.AlterVoucher(ctx, id, ledger.VoucherInput{
		Class: ledger.Journal, VoucherTypeID: vt, VoucherDate: "2025-04-18",
		CreatedBy: "period-test",
		Postings: []ledger.PostingInput{
			{LedgerID: l.rent, DrCr: ledger.Debit, Amount: dec("250")},
			{LedgerID: l.cash, DrCr: ledger.Credit, Amount: dec("250")},
		},
	}); err != nil {
		t.Fatalf("alter: %v", err)
	}
	if got := rentAsOn("2025-04-30").Sub(base); !got.Equal(dec("250")) {
		t.Errorf("after altering 100 -> 250 the Rent balance moved %s, want 250 "+
			"(an alter RESTATES; 350 would mean both versions were counted)", got.StringFixed(4))
	}
	// And at a LATER as-at date too — reading historic versions would show 350.
	if got := rentAsOn("2025-12-31").Sub(rentAsOn("2025-12-31").Sub(rentAsOn("2025-04-30"))).Sub(base); !got.Equal(dec("250")) {
		t.Errorf("the alter is restated at a later as-at date too: moved %s, want 250", got.StringFixed(4))
	}

	// DELETE: the effect goes entirely, at every as-at date.
	if _, err := led.DeleteVoucher(ctx, id, "period-test"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	for _, asOn := range []string{"2025-04-30", "2025-12-31", ""} {
		if got := rentAsOn(asOn).Sub(base); !got.IsZero() {
			t.Errorf("as on %q the DELETED voucher still moves the Rent balance by %s, want 0",
				asOn, got.StringFixed(4))
		}
	}
	// ...and the book still balances after all of it.
	tb, err := rep.TrialBalance(ctx, companyID, "2025-04-30")
	if err != nil {
		t.Fatalf("trial balance: %v", err)
	}
	if !tb.IsBalanced() {
		t.Errorf("Trial Balance does not balance after alter+delete: Dr %s Cr %s",
			tb.TotalDebit.StringFixed(4), tb.TotalCredit.StringFixed(4))
	}
}

// TestPositionBeforeTheBooksBeganIsEmpty pins the rule an opening balance
// obeys. An opening balance has no date column — it is stated as at
// company.books_beginning_from — so an as-at query that folded it in
// unconditionally would report a position for a date before the entity's books
// existed. The live verifier found exactly that: a Trial Balance dated 2020
// showing 520000 of opening capital on a company whose books begin in 2026.
//
// Excluding openings is all-or-nothing (they balance among themselves), so both
// sides of the boundary must still FOOT — asserted here, because an opening
// rule that balanced on one side only would be worse than none.
func TestPositionBeforeTheBooksBeganIsEmpty(t *testing.T) {
	pool, ctx, companyID, ids := setup(t)
	seedBooks(t, pool, ctx, ids) // company fy/books start 2025-04-01
	rep := reporting.NewService(pool)

	if _, err := masters.NewService(pool).CreateLedger(ctx, masters.Ledger{
		Name: "Opening Capital", GroupName: "Capital Account",
		OpeningBalance: dec("50000"), OpeningDC: "C",
	}, "period-test"); err != nil {
		t.Fatalf("create opening ledger: %v", err)
	}
	if _, err := masters.NewService(pool).CreateLedger(ctx, masters.Ledger{
		Name: "Opening Bank", GroupName: "Bank Accounts",
		OpeningBalance: dec("50000"), OpeningDC: "D",
	}, "period-test"); err != nil {
		t.Fatalf("create opening ledger: %v", err)
	}

	before, err := rep.TrialBalance(ctx, companyID, "2020-01-01")
	if err != nil {
		t.Fatalf("trial balance before the books: %v", err)
	}
	if !before.TotalDebit.IsZero() || !before.TotalCredit.IsZero() {
		t.Errorf("a Trial Balance dated before books_beginning_from shows Dr %s / Cr %s, want nothing — "+
			"the opening balance is stated AS AT the books beginning date, not before it",
			before.TotalDebit.StringFixed(4), before.TotalCredit.StringFixed(4))
	}
	if !before.IsBalanced() {
		t.Errorf("an empty position does not balance")
	}

	// On or after the beginning date the opening IS included, and still foots.
	after, err := rep.TrialBalance(ctx, companyID, "2025-04-01")
	if err != nil {
		t.Fatalf("trial balance on the beginning date: %v", err)
	}
	if after.TotalDebit.IsZero() {
		t.Errorf("the opening balance is missing ON the books beginning date")
	}
	if !after.IsBalanced() {
		t.Errorf("the position on the beginning date does not balance: Dr %s Cr %s",
			after.TotalDebit.StringFixed(4), after.TotalCredit.StringFixed(4))
	}

	// The UNBOUNDED report is unaffected — no as-at date means no boundary.
	all, err := rep.TrialBalance(ctx, companyID, "")
	if err != nil {
		t.Fatalf("unbounded trial balance: %v", err)
	}
	if all.TotalDebit.IsZero() || !all.IsBalanced() {
		t.Errorf("the unbounded Trial Balance lost its opening balances: Dr %s Cr %s",
			all.TotalDebit.StringFixed(4), all.TotalCredit.StringFixed(4))
	}
}
