package insights_test

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/insights"
	"github.com/khata/cored/internal/ledger"
)

// drain processes every pending voucher event of the book's company.
func (b *book) drain() insights.EventStats {
	b.t.Helper()
	total := insights.EventStats{Raised: map[string]int{}}
	for i := 0; i < 50; i++ {
		st, err := insights.ProcessEvents(b.ctx, b.pool, b.cid, time.Now(), 200)
		if err != nil {
			b.t.Fatalf("process events: %v", err)
		}
		total.Events += st.Events
		total.Skipped += st.Skipped
		for k, v := range st.Raised {
			total.Raised[k] += v
		}
		total.Refused = append(total.Refused, st.Refused...)
		if st.Events == 0 {
			return total
		}
	}
	b.t.Fatal("event queue never drained")
	return total
}

func (b *book) night(asOf time.Time) insights.Stats {
	b.t.Helper()
	st, err := insights.RunNightly(b.ctx, b.pool, b.cid, asOf)
	if err != nil {
		b.t.Fatalf("nightly %s: %v", asOf.Format("2006-01-02"), err)
	}
	return st
}

func (b *book) open() []insights.Insight {
	b.t.Helper()
	in, err := insights.List(b.ctx, b.pool, insights.ListFilter{Limit: 500})
	if err != nil {
		b.t.Fatalf("list: %v", err)
	}
	return in
}

// verifyAll re-runs the evidence of every open insight in a READ ONLY
// transaction and returns the types it proved.
func (b *book) verifyAll() map[string]int {
	b.t.Helper()
	proved := map[string]int{}
	for _, in := range b.open() {
		if err := insights.VerifyStored(b.ctx, b.pool, in.ID); err != nil {
			b.t.Errorf("EVIDENCE DOES NOT REPRODUCE %s %q: %v", in.Type, in.EntityName, err)
			continue
		}
		proved[in.Type]++
	}
	return proved
}

// TestEveryInsightReproducesItsNumbers is W4's first gate: over a book in
// which every detector has something true to find, every open insight's
// evidence_sql, re-run read-only, returns every payload figure EQUAL to the
// paisa and exactly its voucher_ids — and every one of the 15 built types is
// among them, so the gate cannot pass by producing nothing.
func TestEveryInsightReproducesItsNumbers(t *testing.T) {
	b := newBook(t)
	b.seed()

	ev := b.drain()
	t.Logf("real-time: %d events, %d skipped, outcomes %v", ev.Events, ev.Skipped, ev.Raised)
	if len(ev.Refused) > 0 {
		t.Errorf("real-time evidence refused at write time: %v", ev.Refused)
	}
	proved := b.verifyAll()

	st1 := b.night(b.T)
	t.Logf("night 1 (%s): %d profile facts, by type %v, expired %d, revalidated %d",
		st1.AsOf, st1.ProfileFacts, st1.ByType, st1.Expired, st1.Revalidated)
	if len(st1.EvidenceRefused) > 0 {
		t.Errorf("night 1 refused: %v", st1.EvidenceRefused)
	}
	for k, v := range b.verifyAll() {
		proved[k] += v
	}

	// Night 2 on the 5th of next month: last month's GSTR-3B is due.
	t2 := time.Date(b.T.Year(), b.T.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, 1, 4)
	st2 := b.night(t2)
	t.Logf("night 2 (%s): by type %v, expired %d", st2.AsOf, st2.ByType, st2.Expired)
	if len(st2.EvidenceRefused) > 0 {
		t.Errorf("night 2 refused: %v", st2.EvidenceRefused)
	}
	for k, v := range b.verifyAll() {
		proved[k] += v
	}

	var missing []string
	for _, td := range insights.Types {
		if proved[td.Code] == 0 {
			missing = append(missing, td.Code)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("no verified insight of type(s) %v — the seed must give every detector something true to find", missing)
	}
	n := 0
	for _, v := range proved {
		n += v
	}
	t.Logf("verified %d insight rows across %d types, every figure equal to the paisa", n, len(proved))
}

// TestEvidenceGateCatchesATamperedPayload proves Verify is not vacuous: the
// same stored insight, with one figure moved by one paisa, or one voucher id
// dropped, fails.
func TestEvidenceGateCatchesATamperedPayload(t *testing.T) {
	b := newBook(t)
	b.seed()
	b.drain()
	var target *insights.Insight
	for _, in := range b.open() {
		if in.Type == insights.TypeCreditLimitBreach {
			in := in
			target = &in
		}
	}
	if target == nil {
		t.Fatal("no credit-limit insight to tamper with")
	}
	c := target.Candidate()
	check := func(name string, c insights.Candidate) error {
		return b.pool.RunInReadOnlyTx(b.ctx, func(ctx context.Context, tx pgx.Tx) error {
			return insights.Verify(ctx, tx, c)
		})
	}
	if err := check("as stored", c); err != nil {
		t.Fatalf("the stored insight does not verify: %v", err)
	}
	moved := map[string]string{}
	for k, v := range c.Payload {
		moved[k] = v
	}
	moved["excess"] = dec(moved["excess"]).Add(dec("0.01")).StringFixed(2)
	c2 := c
	c2.Payload = moved
	if err := check("one paisa", c2); !errors.Is(err, insights.ErrEvidenceMismatch) {
		t.Errorf("a payload one paisa off verified: %v", err)
	}
	c3 := c
	c3.VoucherIDs = c.VoucherIDs[1:]
	if err := check("dropped voucher", c3); !errors.Is(err, insights.ErrEvidenceMismatch) {
		t.Errorf("a voucher list missing one id verified: %v", err)
	}
}

// TestNightlyIsIdempotentPerCompanyAndDay: a second run on the same business
// day finds insight_run and does nothing.
func TestNightlyIsIdempotentPerCompanyAndDay(t *testing.T) {
	b := newBook(t)
	b.seed()
	b.drain()
	st1 := b.night(b.T)
	if st1.AlreadyRan {
		t.Fatal("the first night reported already-ran")
	}
	before := len(b.open())
	st2 := b.night(b.T)
	if !st2.AlreadyRan {
		t.Fatal("a second run on the same business day ran again")
	}
	if after := len(b.open()); after != before {
		t.Errorf("open insights %d -> %d after a repeated night", before, after)
	}
	var runs int
	if err := b.pool.RunInTx(b.ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM insight_run WHERE run_date = $1`, b.T).Scan(&runs)
	}); err != nil {
		t.Fatal(err)
	}
	if runs != 1 {
		t.Errorf("insight_run rows for the day = %d, want 1", runs)
	}
}

// TestAVoucherEnqueuesExactlyOneEventPerVersion: the post path's only W4
// work is the trigger's one row; the detectors run when the queue drains.
func TestAVoucherEnqueuesExactlyOneEventPerVersion(t *testing.T) {
	b := newBook(t)
	b.post("cap", ledgerJournal(b, "1000"))
	var n int
	if err := b.pool.RunInTx(b.ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM insight_voucher_event WHERE voucher_id = $1`, b.vouchers["cap"]).Scan(&n)
	}); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("events for one posted voucher = %d, want 1", n)
	}
	st := b.drain()
	if st.Events != 1 {
		t.Errorf("drained %d events, want 1", st.Events)
	}
	if again := b.drain(); again.Events != 0 {
		t.Errorf("a processed event was processed again (%d)", again.Events)
	}
}

// TestFeedbackMovesPRealAndSuppressionFilters: a suppressed type leaves the
// digest; "wrong" dismisses the insight and three of them take a 7:3-prior
// type below the digest floor.
func TestFeedbackMovesPRealAndSuppressionFilters(t *testing.T) {
	b := newBook(t)
	b.seed()
	b.drain()
	b.night(b.T)
	items := b.digestItems("")
	if len(items) == 0 || len(items) > insights.MaxDigestItems {
		t.Fatalf("digest has %d items", len(items))
	}
	types := map[string]bool{}
	for _, it := range items {
		types[it.Type] = true
		if it.Score <= 0 || it.PReal < insights.DigestMinPReal {
			t.Errorf("item %s score %v p_real %v", it.Type, it.Score, it.PReal)
		}
	}
	top := items[0]
	// Suppress the top item's type: it must leave the digest.
	if _, err := insights.Suppress(b.ctx, b.pool, b.cid, top.Type, "", nil, "not now", "owner"); err != nil {
		t.Fatal(err)
	}
	for _, it := range b.digestItems("") {
		if it.Type == top.Type {
			t.Errorf("suppressed type %s still in the digest", top.Type)
		}
	}
	// Three "wrong" verdicts on a type with prior 7:3 -> p_real 7/13 < 0.70.
	var victim insights.Insight
	for _, in := range b.open() {
		if in.Type == insights.TypeCustomerReorderDue {
			victim = in
		}
	}
	if victim.ID == uuid.Nil {
		t.Fatal("no reorder-due insight")
	}
	for i := 0; i < 3; i++ {
		if err := insights.Feedback(b.ctx, b.pool, b.cid, victim.ID, insights.VerdictWrong, "", "owner"); err != nil {
			t.Fatal(err)
		}
	}
	got, err := insights.Get(b.ctx, b.pool, victim.ID)
	if err != nil || got.State != "dismissed" || got.Verdicts["wrong"] != 3 {
		t.Fatalf("after 3 wrong: state %s verdicts %v err %v", got.State, got.Verdicts, err)
	}
	td, _ := insights.ByCode(insights.TypeCustomerReorderDue)
	if p := insights.PReal(td, insights.Verdicts{Wrong: 3}); p >= insights.DigestMinPReal {
		t.Errorf("p_real after 3 wrong = %v, want < %v", p, insights.DigestMinPReal)
	}
	if err := insights.Feedback(b.ctx, b.pool, b.cid, victim.ID, "maybe", "", "owner"); !errors.Is(err, insights.ErrBadVerdict) {
		t.Errorf("an unknown verdict was accepted: %v", err)
	}
}

func (b *book) digestItems(recipient string) []insights.DigestItem {
	b.t.Helper()
	var items []insights.DigestItem
	if err := b.pool.RunInReadOnlyTx(b.ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		items, err = insights.SelectDigest(ctx, tx, b.T, recipient)
		return err
	}); err != nil {
		b.t.Fatalf("select digest: %v", err)
	}
	return items
}

// TestDigestIsStoredOnceAndNoveltyDecays: composing twice keeps the first,
// and the next day halves the novelty of every fingerprint already shown.
func TestDigestIsStoredOnceAndNoveltyDecays(t *testing.T) {
	b := newBook(t)
	b.seed()
	b.drain()
	b.night(b.T)
	d1, err := insights.ComposeDigest(b.ctx, b.pool, b.cid, b.T, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if d1.Narrator != "template" || len(d1.Items) == 0 || len(d1.Items) > 5 {
		t.Fatalf("digest narrator %s items %d", d1.Narrator, len(d1.Items))
	}
	for _, it := range d1.Items {
		if strings.Contains(it.Text, "?") && !strings.Contains(it.Payload["party"], "?") {
			t.Errorf("template left a placeholder unrendered: %s", it.Text)
		}
	}
	d1b, err := insights.ComposeDigest(b.ctx, b.pool, b.cid, b.T, "", nil)
	if err != nil || d1b.ID != d1.ID {
		t.Fatalf("second compose made a new digest (%v, %v)", d1b.ID, err)
	}
	var next []insights.DigestItem
	if err := b.pool.RunInReadOnlyTx(b.ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		next, err = insights.SelectDigest(ctx, tx, b.T.AddDate(0, 0, 1), "")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	shown := map[string]bool{}
	for _, it := range d1.Items {
		shown[it.Fingerprint] = true
	}
	for _, it := range next {
		if shown[it.Fingerprint] && it.Novelty != 0.5 {
			t.Errorf("%s shown yesterday has novelty %v, want 0.5", it.Fingerprint, it.Novelty)
		}
	}
}

func ledgerJournal(b *book, amount string) ledger.VoucherInput {
	return ledger.VoucherInput{Class: ledger.Journal, VoucherDate: b.day(0),
		Postings: []ledger.PostingInput{{LedgerID: b.bank, DrCr: ledger.Debit, Amount: dec(amount)},
			{LedgerID: b.capital, DrCr: ledger.Credit, Amount: dec(amount)}}}
}
