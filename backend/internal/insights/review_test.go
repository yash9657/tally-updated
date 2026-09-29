package insights_test

// review_test.go — the wave-2 review's insight-engine findings, each as the
// scenario that broke. Mutation proofs are listed per test.

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/insights"
	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/platform/db"
)

func openOfType(b *book, typ string) []insights.Insight {
	var out []insights.Insight
	for _, in := range b.open() {
		if in.Type == typ {
			out = append(out, in)
		}
	}
	return out
}

// TestAPartPaymentDoesNotWithdrawATrueBreach: Ganga is ~Rs 59k over a
// Rs 10,000 limit; a Rs 1,000 receipt changes the balance (so the old
// evidence no longer reproduces), but the breach is still true. Before the
// fix the night expired it and nothing raised it again.
//
// Mutations (run for the review): the debit-only filter restored in
// detectCreditLimit AND retire()'s re-derivation dropped — the breach
// disappears after the receipt (0 open, expired 1). Re-derivation dropped
// alone — the limit-change step fails (no voucher, so only the night can
// re-judge it). The debit-only filter alone is NOT caught: the night's
// re-derivation rescues the receipt case too (two roads, deliberately).
func TestAPartPaymentDoesNotWithdrawATrueBreach(t *testing.T) {
	b := newBook(t)
	b.seed()
	b.drain()
	b.night(b.T)
	before := openOfType(b, insights.TypeCreditLimitBreach)
	if len(before) != 1 {
		t.Fatalf("seed: %d open credit-limit breaches, want 1", len(before))
	}

	b.settle(ledger.Receipt, b.ganga, b.day(0), "GS-1", dec("1000"))
	b.drain()
	st := b.night(b.T.AddDate(0, 0, 1))
	after := openOfType(b, insights.TypeCreditLimitBreach)
	if len(after) != 1 {
		t.Fatalf("after a Rs 1,000 part-payment: %d open breaches (night expired %d), want 1 — the customer is still far over the limit",
			len(after), st.Expired)
	}
	if after[0].Payload["balance"] == before[0].Payload["balance"] {
		t.Errorf("the breach still shows the pre-receipt balance %s", after[0].Payload["balance"])
	}

	// A change with no voucher at all — the limit raised, still below the
	// balance — is re-derived by the night, not withdrawn.
	b.exec(`UPDATE ledger SET credit_limit = 20000 WHERE id = $1`, b.ganga)
	b.night(b.T.AddDate(0, 0, 2))
	if got := openOfType(b, insights.TypeCreditLimitBreach); len(got) != 1 || got[0].Payload["credit_limit"] != "20000.00" {
		t.Fatalf("after raising the limit to 20,000: %v, want one breach at the new limit", got)
	}

	// And the control: a limit above the balance clears it.
	b.exec(`UPDATE ledger SET credit_limit = 1000000 WHERE id = $1`, b.ganga)
	b.night(b.T.AddDate(0, 0, 3))
	if got := openOfType(b, insights.TypeCreditLimitBreach); len(got) != 0 {
		t.Fatalf("CONTROL: a breach that cleared is still open: %v", got)
	}
}

// TestAnEarlierNightDoesNotExpireLaterInsights: night(T) then night(T-1)
// (an operator backfilling a missed day). The second run's facts are behind
// (profile.ErrAsOfBehind), so its fact-backed detectors find nothing — which
// is not evidence that anything cleared.
//
// Mutations (run for the review): drop retire()'s as_of guard — "a backfill
// night expired 1"; drop both it and the facts-behind guard —
// customer_slower 1 -> 0, expired 5.
func TestAnEarlierNightDoesNotExpireLaterInsights(t *testing.T) {
	b := newBook(t)
	b.seed()
	b.drain()
	b.night(b.T)
	slow := len(openOfType(b, insights.TypeCustomerSlower))
	if slow == 0 {
		t.Fatal("seed: no customer_slower insight to protect; the test would be vacuous")
	}
	st := b.night(b.T.AddDate(0, 0, -1))
	if st.ProfileFacts != -1 {
		t.Logf("note: the earlier night's facts were not behind (profile_facts %d)", st.ProfileFacts)
	}
	if got := len(openOfType(b, insights.TypeCustomerSlower)); got != slow {
		t.Fatalf("customer_slower %d -> %d after a backfill night (expired %d)", slow, got, st.Expired)
	}
	if st.Expired != 0 {
		t.Errorf("a backfill night expired %d insight(s) detected at a later date", st.Expired)
	}
}

// TestTheMorningDigestIsNeverLost: "run now" (RunNightly without a digest)
// used to drop the company from the poller's due list, so that day's digest
// was never composed; and a digest composed before the night ran froze the
// day without that night's findings.
//
// Mutations: drop the poller's digest-due pass — "run now then poller"
// composes 0; drop ComposeDigest's nightly check — the early compose stores a
// digest.
func TestTheMorningDigestIsNeverLost(t *testing.T) {
	b := newBook(t)
	b.seed()
	b.drain()
	ds := b.T.Format("2006-01-02")

	if _, err := insights.ComposeDigest(b.ctx, b.pool, b.cid, b.T, "", nil); !errors.Is(err, insights.ErrNightlyNotRun) {
		t.Fatalf("a digest composed before its night = %v, want ErrNightlyNotRun", err)
	}
	if _, err := insights.GetDigest(b.ctx, b.pool, ds, ""); !errors.Is(err, insights.ErrNoDigest) {
		t.Fatalf("the refused compose stored a digest (%v)", err)
	}

	// What POST /insights/run does, then the poller's tick.
	if _, err := insights.RunNightly(b.ctx, b.pool, b.cid, b.T); err != nil {
		t.Fatal(err)
	}
	p := &insights.Poller{Pool: b.pool}
	if _, err := p.NightlyFor(db.WithCompany(b.ctx, b.cid), b.T); err != nil {
		t.Fatal(err)
	}
	d, err := insights.GetDigest(b.ctx, b.pool, ds, "")
	if err != nil {
		t.Fatalf("after run-now and the poller's tick, the day has no digest: %v", err)
	}
	if len(d.Items) == 0 {
		t.Fatal("the digest has no items though the night found insights")
	}
}

// TestEvidenceVoucherIDsAddUpToTheFigures: the W4 contract is that an
// insight's voucher_ids reproduce its numbers. Before the review the credit
// breach listed only the 200 most recent vouchers (the balance summed them
// all), and customer_slower listed only the raising invoices of bills that
// receipts had part-paid.
//
// Mutations (run for the review): LIMIT 200 restored on the credit-limit
// list turns the "sum of the listed vouchers" check red once the party has
// more than 200; customer_slower's list back to the raising invoices only
// turns the receipt check red.
func TestEvidenceVoucherIDsAddUpToTheFigures(t *testing.T) {
	b := newBook(t)
	b.seed()
	// A part-payment against one of Kaveri's overdue bills (KT-5).
	b.settle(ledger.Receipt, b.kaveri, b.day(-100), "KT-5", dec("100"))
	// 201 small debits to Ganga: more than the old 200 cap.
	for i := 0; i < 201; i++ {
		b.post("", ledger.VoucherInput{Class: ledger.Journal, VoucherDate: b.day(0),
			Postings: []ledger.PostingInput{{LedgerID: b.ganga, DrCr: ledger.Debit, Amount: dec("1")},
				{LedgerID: b.sales, DrCr: ledger.Credit, Amount: dec("1")}}})
	}
	b.drain()
	b.night(b.T)

	var breach, slow *insights.Insight
	for _, in := range b.open() {
		in := in
		switch in.Type {
		case insights.TypeCreditLimitBreach:
			breach = &in
		case insights.TypeCustomerSlower:
			slow = &in
		}
	}
	if breach == nil || slow == nil {
		t.Fatalf("seed: breach %v, slower %v", breach != nil, slow != nil)
	}
	sum := func(sql string, args ...any) string {
		var s string
		if err := b.pool.RunInReadOnlyTx(b.ctx, func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, sql, args...).Scan(&s)
		}); err != nil {
			t.Fatalf("sum: %v", err)
		}
		return s
	}
	// Balance = opening + the party's legs on exactly the listed vouchers.
	got := sum(`SELECT to_char(
	             (SELECT CASE WHEN opening_dc = 'D' THEN opening_balance ELSE -opening_balance END FROM ledger WHERE id = $1)
	           + COALESCE((SELECT SUM(CASE WHEN p.dr_cr = 'D' THEN p.amount ELSE -p.amount END)
	                         FROM posting p JOIN voucher_head vh ON vh.current_version_id = p.voucher_version_id
	                        WHERE p.ledger_id = $1 AND vh.voucher_id = ANY($2)), 0), 'FM999999990.00')`,
		b.ganga, breach.VoucherIDs)
	if got != breach.Payload["balance"] {
		t.Errorf("credit breach: opening + the %d listed vouchers = %s, payload balance %s", len(breach.VoucherIDs), got, breach.Payload["balance"])
	}
	// Overdue amount = the net of the listed vouchers' allocations on the
	// overdue bills, receipts included.
	got = sum(`SELECT to_char(COALESCE(SUM(CASE WHEN ba.dr_cr = 'D' THEN ba.amount ELSE -ba.amount END), 0), 'FM999999990.00')
	             FROM bill_allocation ba JOIN voucher_head vh ON vh.current_version_id = ba.voucher_version_id
	            WHERE ba.ledger_id = $1 AND vh.voucher_id = ANY($2) AND ba.ref_name <> 'On Account'
	              AND ba.ref_name IN (SELECT ba2.ref_name FROM bill_allocation ba2
	                                   JOIN voucher_head vh2 ON vh2.current_version_id = ba2.voucher_version_id
	                                  WHERE vh2.voucher_id = ANY($2) AND ba2.ref_type = 'NEW' AND ba2.ledger_id = $1)`,
		b.kaveri, slow.VoucherIDs)
	if got != slow.Payload["overdue_amount"] {
		t.Errorf("customer_slower: the %d listed vouchers net to %s, payload overdue_amount %s", len(slow.VoucherIDs), got, slow.Payload["overdue_amount"])
	}
}
