package profile_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/platform/testdb"
	"github.com/khata/cored/internal/profile"
	"github.com/khata/cored/internal/query"
	"github.com/khata/cored/internal/reporting"
)

const (
	asOf    = "2026-03-31"
	winFrom = "2025-04-01"
)

// allCurrent reads every current fact through the PUBLIC tool surface's
// storage path (readFacts is unexported; the tools are the API).
func allFacts(t *testing.T, b *book) []profile.StoredFact {
	t.Helper()
	var out []profile.StoredFact
	for _, name := range []string{"Kaveri Traders", "Ganga Stores"} {
		r, err := profile.GetCustomerProfile(b.ctx, b.pool, name, "")
		if err != nil {
			t.Fatalf("customer %s: %v", name, err)
		}
		out = append(out, r.Facts...)
	}
	for _, name := range []string{"Bharat Metals", "Deccan Steel"} {
		r, err := profile.GetSupplierProfile(b.ctx, b.pool, name, "")
		if err != nil {
			t.Fatalf("supplier %s: %v", name, err)
		}
		out = append(out, r.Facts...)
	}
	for _, name := range []string{"Widget", "Gadget", "Bolt"} {
		r, err := profile.GetItemProfile(b.ctx, b.pool, name, "")
		if err != nil {
			t.Fatalf("item %s: %v", name, err)
		}
		out = append(out, r.Facts...)
	}
	r, err := profile.GetRecurringStreams(b.ctx, b.pool, "")
	if err != nil {
		t.Fatalf("streams: %v", err)
	}
	out = append(out, r.Facts...)
	tr, _, err := profile.GetTenantProfile(b.ctx, b.pool, "")
	if err != nil {
		t.Fatalf("tenant: %v", err)
	}
	return append(out, tr.Facts...)
}

func factValue(t *testing.T, fs []profile.StoredFact, entity, key, qualifier string) string {
	t.Helper()
	for _, f := range fs {
		if f.EntityName == entity && f.Key == key && f.QualifierName == qualifier {
			return f.Value
		}
	}
	t.Fatalf("no fact %s/%s[%s]", entity, key, qualifier)
	return ""
}

func eqDec(a string, b decimal.Decimal) bool {
	d, err := decimal.NewFromString(a)
	return err == nil && d.Equal(b)
}

// TestEveryProfileFactReconcilesToItsReport is THE W3 gate: "every profile
// fact reconciles to the rupee with the report it summarises, tested against a
// seeded book".
//
// For EVERY stored fact with reconciles_to set, the named report is re-run
// fresh (its own transaction, after the refresh committed) and the figure
// re-derived from it; a one-paisa difference fails. A (report, fact) pair
// with no reconciler below FAILS rather than passing unchecked, so a new money
// fact cannot dodge the gate by being new. Where an INDEPENDENT SQL path
// exists (the query DSL), the fact is checked against that too — so "taken
// from the report" cannot hide a report that is wrong in the same way.
func TestEveryProfileFactReconcilesToItsReport(t *testing.T) {
	b := newBook(t, "Profile Reconcile")
	b.seed(t)
	res, err := profile.Refresh(b.ctx, b.pool, b.cid, profile.Options{AsOf: asOf})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	t.Logf("refresh wrote %d facts, %d edges, card v%d (%d tokens): %v", res.Facts, res.Edges, res.ProfileVersion, res.ProfileTokens, res.ByJob)

	rep := reporting.NewService(b.pool)
	recv, err := rep.Outstandings(b.ctx, b.cid, reporting.Receivables, asOf, nil)
	if err != nil {
		t.Fatal(err)
	}
	pay, err := rep.Outstandings(b.ctx, b.cid, reporting.Payables, asOf, nil)
	if err != nil {
		t.Fatal(err)
	}
	dayBook, err := rep.DayBook(b.ctx, b.cid, winFrom, asOf)
	if err != nil {
		t.Fatal(err)
	}
	ss, err := rep.StockSummary(b.ctx, b.cid, asOf)
	if err != nil {
		t.Fatal(err)
	}
	mv, err := rep.StockMovementReport(b.ctx, b.cid, winFrom, asOf)
	if err != nil {
		t.Fatal(err)
	}
	partyPending := func(o reporting.Outstanding, id string) decimal.Decimal {
		s := decimal.Zero
		for _, p := range o.Parties {
			if p.LedgerID.String() == id {
				s = s.Add(p.Pending)
			}
		}
		return s
	}
	dayBookLegs := func(class, ledgerID, side string, only map[uuid.UUID]bool) decimal.Decimal {
		s := decimal.Zero
		for _, e := range dayBook.Entries {
			if class != "" && e.Class != class {
				continue
			}
			if only != nil && !only[e.VoucherID] {
				continue
			}
			for _, p := range e.Postings {
				if p.LedgerID.String() == ledgerID && p.DrCr == side {
					s = s.Add(p.Amount)
				}
			}
		}
		return s
	}
	itemVouchers := map[string]reporting.StockItemVouchers{}
	siv := func(itemID string) reporting.StockItemVouchers {
		if v, ok := itemVouchers[itemID]; ok {
			return v
		}
		v, err := rep.StockItemVouchers(b.ctx, b.cid, uuid.MustParse(itemID), winFrom, asOf)
		if err != nil {
			t.Fatalf("stock item vouchers: %v", err)
		}
		itemVouchers[itemID] = v
		return v
	}
	plMonth := map[string]reporting.ProfitAndLoss{}
	pl := func(month string) reporting.ProfitAndLoss {
		if v, ok := plMonth[month]; ok {
			return v
		}
		from := month + "-01"
		to := lastDay(t, month)
		v, err := rep.ProfitAndLoss(b.ctx, b.cid, from, to)
		if err != nil {
			t.Fatal(err)
		}
		plMonth[month] = v
		return v
	}

	// The stored facts, with evidence, read straight from the table so every
	// row is covered — not only the ones a tool happens to return.
	type row struct {
		kind, key, name, entityKey, qKey, qName, value, recon string
		evidence                                              []uuid.UUID
	}
	var rows []row
	if err := b.pool.RunInReadOnlyTx(b.ctx, func(ctx context.Context, tx pgx.Tx) error {
		rs, err := tx.Query(ctx, `
			SELECT entity_kind, fact_key, entity_name, entity_key, qualifier_key, qualifier_name,
			       COALESCE(value_num::text, value_text), COALESCE(reconciles_to, ''), evidence_ids
			  FROM entity_facts WHERE superseded_at IS NULL AND valid_to IS NULL`)
		if err != nil {
			return err
		}
		defer rs.Close()
		for rs.Next() {
			var r row
			if err := rs.Scan(&r.kind, &r.key, &r.name, &r.entityKey, &r.qKey, &r.qName, &r.value, &r.recon, &r.evidence); err != nil {
				return err
			}
			rows = append(rows, r)
		}
		return rs.Err()
	}); err != nil {
		t.Fatalf("read facts: %v", err)
	}
	if len(rows) != res.Facts {
		t.Fatalf("table holds %d current facts, refresh reported %d", len(rows), res.Facts)
	}

	reconciled := map[string]int{}
	for _, r := range rows {
		if r.recon == "" {
			continue
		}
		var want decimal.Decimal
		switch r.recon + "/" + r.key {
		case "outstandings/receivable_outstanding":
			want = partyPending(recv, r.entityKey)
		case "outstandings/receivables_total":
			want = recv.Total
		case "bills-payable/payable_outstanding":
			want = partyPending(pay, r.entityKey)
		case "bills-payable/payables_total":
			want = pay.Total
		case "day-book/credit_sales_window":
			want = dayBookLegs("Sales", r.entityKey, "D", nil)
		case "day-book/purchases_window":
			want = dayBookLegs("Purchase", r.entityKey, "C", nil)
		case "day-book/stream_total_window":
			only := map[uuid.UUID]bool{}
			for _, e := range r.evidence {
				only[e] = true
			}
			side := "D"
			if strings.HasPrefix(r.entityKey, "receipt:") {
				side = "C"
			}
			want = dayBookLegs("", r.qKey, side, only)
		case "day-book/vouchers_window":
			want = decimal.NewFromInt(int64(len(dayBook.Entries)))
		case "stock-item-vouchers/item_purchase_amount":
			for _, l := range siv(r.qKey).Lines {
				if l.Direction == "IN" && l.Party == r.name {
					want = want.Add(l.Amount)
				}
			}
		case "stock-item-vouchers/out_amount_window":
			for _, l := range siv(r.entityKey).Lines {
				if l.Direction == "OUT" {
					want = want.Add(l.Amount)
				}
			}
		case "stock-summary/closing_qty", "stock-summary/closing_value":
			for _, it := range ss.Items {
				if it.StockItemID.String() == r.entityKey {
					if r.key == "closing_qty" {
						want = it.ClosingQty
					} else {
						want = it.ClosingValue
					}
				}
			}
		case "stock-movement/in_qty_window", "stock-movement/out_qty_window":
			for _, it := range mv.Items {
				if it.StockItemID.String() == r.entityKey {
					if r.key == "in_qty_window" {
						want = it.InwardQty
					} else {
						want = it.OutwardQty
					}
				}
			}
		case "profit-and-loss/sales_month":
			want = profile.SectionNode(pl(r.qKey).Income, "Sales Accounts")
			// Independent path: the DSL's postings natural_net (D51's measure).
			dsl := dslSum(t, b, query.Query{Entity: query.EntityPostings,
				Metrics:   []query.Metric{{Func: query.AggSum, Column: "natural_net", As: "v"}},
				Filters:   []query.Filter{{Column: "pl_group", Op: query.OpEq, Values: []string{"Sales Accounts"}}},
				DateRange: &query.DateRange{Column: "voucher_date", From: r.qKey + "-01", To: lastDay(t, r.qKey)}})
			if !dsl.Equal(want) {
				t.Errorf("sales %s: P&L %s but the DSL's natural_net %s — the report and the independent path disagree", r.qKey, want, dsl)
			}
		case "profit-and-loss/purchases_month":
			want = profile.SectionNode(pl(r.qKey).Expenses, "Purchase Accounts")
		default:
			t.Errorf("fact %s reconciles to %q but this gate has NO reconciler for that pair — add one; an unchecked money fact is the defect this test exists to stop", r.key, r.recon)
			continue
		}
		if !eqDec(r.value, want) {
			t.Errorf("%s %s[%s] = %s, but %s says %s", r.name, r.key, r.qName, r.value, r.recon, want)
		}
		reconciled[r.key]++
	}
	// Non-vacuity: every reconcilable key in the vocabulary was produced by
	// the seeded book and checked at least once.
	for _, d := range profile.Facts {
		if d.ReconcilesTo != "" && reconciled[d.Key] == 0 {
			t.Errorf("vocabulary fact %s (-> %s) was never reconciled: the seed does not exercise it", d.Key, d.ReconcilesTo)
		}
	}
	total := 0
	for _, n := range reconciled {
		total += n
	}
	t.Logf("reconciled %d facts across %d keys", total, len(reconciled))

	// And because the report-taken figures could agree with a report that is
	// wrong the same way, the HAND-COMPUTED values (book_test.go's comments).
	fs := allFacts(t, b)
	for _, c := range []struct{ entity, key, q, want string }{
		{"Kaveri Traders", "receivable_outstanding", "", "3500"},
		{"Kaveri Traders", "credit_sales_window", "", "19500"},
		{"Kaveri Traders", "dso_days", "", "65.5"},
		{"Kaveri Traders", "receipt_lag_p50_days", "", "31"},
		{"Kaveri Traders", "receipt_lag_p90_days", "", "31"},
		{"Kaveri Traders", "receipt_lag_mean_days", "", "30.6"},
		{"Kaveri Traders", "bills_settled_count", "", "10"},
		{"Ganga Stores", "receivable_outstanding", "", "3000"},
		{"Ganga Stores", "dso_days", "", "121.7"},
		{"Ganga Stores", "receipt_lag_p50_days", "", "60"},
		{"Ganga Stores", "receipt_lag_p90_days", "", "95"},
		{"Bharat Metals", "payable_outstanding", "", "0"},
		{"Bharat Metals", "payment_lag_p50_days", "", "45"},
		{"Bharat Metals", "item_price_drift_pct", "Widget", "10"},
		{"Bharat Metals", "item_purchase_amount", "Widget", "15700"},
		{"Deccan Steel", "payable_outstanding", "", "21500"},
		{"Deccan Steel", "item_price_drift_pct", "Gadget", "-5"},
		{"Widget", "closing_qty", "", "60"},
		{"Widget", "out_qty_window", "", "240"},
		{"Widget", "velocity_qty_per_day", "", "0.6575"},
		{"Widget", "days_of_cover", "", "91.3"},
		{"Widget", "abc_class", "", "A"},
		{"Widget", "xyz_class", "", "X"},
		{"Gadget", "abc_class", "", "A"},
		{"Gadget", "xyz_class", "", "Z"},
		{"Gadget", "monthly_out_cv", "", "1.7321"},
		{"Bolt", "abc_class", "", "C"},
		{"Bolt", "closing_qty", "", "900"},
		{"Rent Paid (paid)", "stream_occurrences", "Rent Paid", "12"},
		{"Rent Paid (paid)", "stream_total_window", "Rent Paid", "300500"},
		{"Rent Paid (paid)", "stream_median_amount", "Rent Paid", "25000"},
		{"Rent Paid (paid)", "stream_cadence", "Rent Paid", "monthly"},
		{"Rent Paid (paid)", "stream_next_due_in_days", "Rent Paid", "5"},
		{"Kaveri Traders (received)", "stream_occurrences", "Kaveri Traders", "10"},
		{"company", "sales_month", "2025-04", "4600"},
		{"company", "sales_month", "2025-08", "1900"},
		{"company", "purchases_month", "2025-04", "15000"},
		{"company", "sales_run_rate_3m", "", "1600"},
		{"company", "sales_run_rate_12m", "", "2375"},
		{"company", "sales_seasonality_index", "04", "1.9368"},
		{"company", "receivables_total", "", "6500"},
		{"company", "payables_total", "", "21500"},
		{"company", "vouchers_window", "", "50"},
	} {
		got := factValue(t, fs, c.entity, c.key, c.q)
		if gd, err := decimal.NewFromString(got); err == nil {
			if !gd.Equal(dec(c.want)) {
				t.Errorf("%s %s[%s] = %s, hand-computed %s", c.entity, c.key, c.q, got, c.want)
			}
		} else if got != c.want {
			t.Errorf("%s %s[%s] = %q, hand-computed %q", c.entity, c.key, c.q, got, c.want)
		}
	}
	// No false stream: Bharat's three payments (5000/5200/5500) are outside ±5%.
	for _, f := range fs {
		if f.Kind == profile.KindStream && strings.HasPrefix(f.EntityName, "Bharat") {
			t.Errorf("Bharat's payments differ by more than 5%% and must not form a stream: %+v", f)
		}
	}
}

func lastDay(t *testing.T, month string) string {
	t.Helper()
	switch month[5:] {
	case "04", "06", "09", "11":
		return month + "-30"
	case "02":
		return month + "-28" // 2026 is not a leap year; the seed's only February
	default:
		return month + "-31"
	}
}

func dslSum(t *testing.T, b *book, q query.Query) decimal.Decimal {
	t.Helper()
	r, err := query.Execute(b.ctx, b.pool, q)
	if err != nil {
		t.Fatalf("dsl: %v", err)
	}
	return dec(r.Rows[0][0])
}

// TestEveryToolEqualsItsDSLTwin: for every tool and entity, the tool's facts
// and the profile_facts DSL rows are the same set with the same values — the
// "each also exposed as a DSL report so the UI shows the same numbers" gate.
// And every tool's text is within the 1k-token cap, the card within 1.5k.
//
// NON-VACUOUS: drop the valid_to guard from the DSL entity and the second
// refresh's closed copies double every DSL row (TestBiTemporal... refreshes
// twice before calling this helper; here the check is set EQUALITY, so an
// extra row on either side fails).
func TestEveryToolEqualsItsDSLTwin(t *testing.T) {
	b := newBook(t, "Profile Tools")
	b.seed(t)
	// Two refreshes, so closed historical copies exist and a DSL that forgot
	// valid_to IS NULL would show them.
	if _, err := profile.Refresh(b.ctx, b.pool, b.cid, profile.Options{AsOf: "2026-03-30"}); err != nil {
		t.Fatal(err)
	}
	if _, err := profile.Refresh(b.ctx, b.pool, b.cid, profile.Options{AsOf: asOf}); err != nil {
		t.Fatal(err)
	}
	type tc struct {
		tool, entity string
		call         func() (profile.ToolResult, error)
	}
	cases := []tc{
		{profile.ToolCustomerProfile, "Kaveri Traders", func() (profile.ToolResult, error) {
			return profile.GetCustomerProfile(b.ctx, b.pool, "kaveri traders", "")
		}},
		{profile.ToolSupplierProfile, "Bharat Metals", func() (profile.ToolResult, error) {
			return profile.GetSupplierProfile(b.ctx, b.pool, "Bharat Metals", "")
		}},
		{profile.ToolItemProfile, "Widget", func() (profile.ToolResult, error) { return profile.GetItemProfile(b.ctx, b.pool, "Widget", "") }},
		{profile.ToolRecurring, "", func() (profile.ToolResult, error) { return profile.GetRecurringStreams(b.ctx, b.pool, "") }},
		{profile.ToolTenantProfile, "", func() (profile.ToolResult, error) {
			r, _, err := profile.GetTenantProfile(b.ctx, b.pool, "")
			return r, err
		}},
	}
	for _, c := range cases {
		tr, err := c.call()
		if err != nil {
			t.Fatalf("%s: %v", c.tool, err)
		}
		if len(tr.Facts) == 0 {
			t.Fatalf("%s returned no facts — the comparison would be vacuous", c.tool)
		}
		if tr.Tokens > profile.ToolTokenCap || profile.EstimateTokens(tr.Text) > profile.ToolTokenCap {
			t.Errorf("%s text is %d tokens, cap %d", c.tool, tr.Tokens, profile.ToolTokenCap)
		}
		q, err := profile.ToolDSL(c.tool, c.entity)
		if err != nil {
			t.Fatal(err)
		}
		res, err := query.Execute(b.ctx, b.pool, q)
		if err != nil {
			t.Fatalf("%s DSL: %v", c.tool, err)
		}
		// dims: entity_name, fact_key, qualifier_name, unit, value_text; metric value.
		dslSet := map[string]string{}
		for _, r := range res.Rows {
			v := r[5]
			if r[3] == "class" {
				v = r[4]
			}
			dslSet[r[0]+"|"+r[1]+"|"+r[2]] = v
		}
		toolSet := map[string]string{}
		for _, f := range tr.Facts {
			toolSet[f.EntityName+"|"+f.Key+"|"+f.QualifierName] = f.Value
		}
		if len(dslSet) != len(toolSet) {
			t.Errorf("%s: tool has %d facts, DSL %d", c.tool, len(toolSet), len(dslSet))
		}
		for k, v := range toolSet {
			if dslSet[k] != v {
				t.Errorf("%s %s: tool %q, DSL %q", c.tool, k, v, dslSet[k])
			}
		}
	}
	_, card, err := profile.GetTenantProfile(b.ctx, b.pool, "")
	if err != nil {
		t.Fatal(err)
	}
	if card.TokenEstimate > profile.TenantProfileTokenCap || profile.EstimateTokens(card.Text) != card.TokenEstimate {
		t.Errorf("card estimate %d (recomputed %d), cap %d", card.TokenEstimate, profile.EstimateTokens(card.Text), profile.TenantProfileTokenCap)
	}
	for _, want := range []string{"Receivables: Rs 6500.00", "Kaveri Traders", "Rent Paid (paid)", "Widget"} {
		if !strings.Contains(card.Text, want) {
			t.Errorf("card is missing %q:\n%s", want, card.Text)
		}
	}
	t.Logf("tenant card v%d, %d tokens:\n%s", card.Version, card.TokenEstimate, card.Text)
}

// TestBiTemporalHistoryIsKept: a second refresh at a later as-of closes the
// first set (a closed copy [d1, d2)) and a recompute at the SAME as-of
// supersedes without a copy; as_of reads return the belief for that date; an
// earlier as-of is refused; the table refuses an in-place edit.
func TestBiTemporalHistoryIsKept(t *testing.T) {
	b := newBook(t, "Profile Bitemporal")
	b.seed(t)
	if _, err := profile.Refresh(b.ctx, b.pool, b.cid, profile.Options{AsOf: "2026-03-20"}); err != nil {
		t.Fatal(err)
	}
	// Kaveri pays February's bill on the 25th: her receivable moves.
	b.receipt(t, b.kaveri, "2026-03-25", "KT-11", "1600")
	if _, err := profile.Refresh(b.ctx, b.pool, b.cid, profile.Options{AsOf: asOf}); err != nil {
		t.Fatal(err)
	}
	if _, err := profile.Refresh(b.ctx, b.pool, b.cid, profile.Options{AsOf: asOf}); err != nil {
		t.Fatal(err) // same as-of again: a correction, no new history
	}

	old, err := profile.GetCustomerProfile(b.ctx, b.pool, "Kaveri Traders", "2026-03-25")
	if err != nil {
		t.Fatal(err)
	}
	cur, err := profile.GetCustomerProfile(b.ctx, b.pool, "Kaveri Traders", "")
	if err != nil {
		t.Fatal(err)
	}
	vOld := factValue(t, old.Facts, "Kaveri Traders", "receivable_outstanding", "")
	vCur := factValue(t, cur.Facts, "Kaveri Traders", "receivable_outstanding", "")
	if vOld == vCur {
		t.Fatalf("history lost: the 03-20 belief (%s) equals the current one (%s) after a receipt moved it", vOld, vCur)
	}
	if !eqDec(vCur, dec("1900")) {
		t.Errorf("current receivable = %s, want 1900 (Mar 1600 + Bolt 300)", vCur)
	}
	if !eqDec(vOld, dec("3500")) {
		t.Errorf("03-20 receivable = %s, want 3500 (Feb 1600 + Mar 1600 + Bolt 300)", vOld)
	}
	if old.Facts[0].ValidTo != asOf {
		t.Errorf("the 03-20 belief should be closed at %s, valid_to=%q", asOf, old.Facts[0].ValidTo)
	}

	// Row census: for one fact, exactly one closed copy and one current row
	// believed now; the superseded originals and the same-day correction are
	// retained as superseded (transaction-time history).
	var believedNow, superseded int
	if err := b.pool.RunInReadOnlyTx(b.ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT count(*) FILTER (WHERE superseded_at IS NULL), count(*) FILTER (WHERE superseded_at IS NOT NULL)
			  FROM entity_facts WHERE entity_name = 'Kaveri Traders' AND fact_key = 'receivable_outstanding'`).Scan(&believedNow, &superseded)
	}); err != nil {
		t.Fatal(err)
	}
	if believedNow != 2 || superseded != 2 {
		t.Errorf("want 2 believed-now rows ([03-20,03-31) closed copy + current) and 2 superseded (03-20 original, first 03-31); got %d/%d", believedNow, superseded)
	}

	if _, err := profile.Refresh(b.ctx, b.pool, b.cid, profile.Options{AsOf: "2026-03-01"}); !errors.Is(err, profile.ErrAsOfBehind) {
		t.Errorf("an earlier as-of must be refused with ErrAsOfBehind, got %v", err)
	}

	// The table itself refuses an in-place edit (0096's trigger) — carry the
	// outcome out of the closure, never Fatal inside it (HANDOFF §7.1).
	var editErr, deleteErr error
	_ = b.pool.RunInTx(b.ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, editErr = tx.Exec(ctx, `UPDATE entity_facts SET value_num = 1 WHERE superseded_at IS NULL`)
		return errors.New("rollback")
	})
	_ = b.pool.RunInTx(b.ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, deleteErr = tx.Exec(ctx, `DELETE FROM entity_facts`)
		return errors.New("rollback")
	})
	if editErr == nil || !strings.Contains(editErr.Error(), "only permitted UPDATE") {
		t.Errorf("an in-place value edit must be refused by the trigger; got %v", editErr)
	}
	if deleteErr == nil {
		t.Error("DELETE must be refused (no grant, and the trigger)")
	}
}

// TestProfilesAreTenantScoped: company B, with no refresh of its own, sees
// none of company A's facts through the tools, the DSL or the raw table.
func TestProfilesAreTenantScoped(t *testing.T) {
	a := newBook(t, "Profile Tenant A")
	a.seed(t)
	if _, err := profile.Refresh(a.ctx, a.pool, a.cid, profile.Options{AsOf: asOf}); err != nil {
		t.Fatal(err)
	}
	cidB := uuid.Must(uuid.NewV7())
	ctxB := db.WithCompany(context.Background(), cidB)
	if _, err := identity.NewService(a.pool).CreateCompany(ctxB, identity.Company{ID: cidB, Name: "Profile Tenant B " + cidB.String()[:8], FYStartDate: "2025-04-01"}); err != nil {
		t.Fatal(err)
	}
	testdb.Cleanup(t, cidB)

	if _, err := profile.GetCustomerProfile(a.ctx, a.pool, "Kaveri Traders", ""); err != nil {
		t.Fatalf("A must see its own profile: %v", err)
	}
	if _, err := profile.GetCustomerProfile(ctxB, a.pool, "Kaveri Traders", ""); !errors.Is(err, profile.ErrNotFound) {
		t.Errorf("B must not find A's customer; got %v", err)
	}
	if _, _, err := profile.GetTenantProfile(ctxB, a.pool, ""); !errors.Is(err, profile.ErrNoProfile) {
		t.Errorf("B must not read A's card; got %v", err)
	}
	r, err := query.Execute(ctxB, a.pool, query.Query{Entity: query.EntityProfileFacts,
		Metrics: []query.Metric{{Func: query.AggCount, Column: "*", As: "n"}}})
	if err != nil {
		t.Fatal(err)
	}
	if r.Rows[0][0] != "0.0000" && r.Rows[0][0] != "0" {
		t.Errorf("B's profile_facts count = %s, want 0", r.Rows[0][0])
	}
	ra, _ := query.Execute(a.ctx, a.pool, query.Query{Entity: query.EntityProfileFacts,
		Metrics: []query.Metric{{Func: query.AggCount, Column: "*", As: "n"}}})
	if ra.Rows[0][0] == "0" || ra.Rows[0][0] == "0.0000" {
		t.Fatal("A's own count is zero — the probe is vacuous")
	}
	// Writing A's company id from B's session is refused by WITH CHECK.
	var insErr error
	_ = a.pool.RunInTx(ctxB, func(ctx context.Context, tx pgx.Tx) error {
		_, insErr = tx.Exec(ctx, `INSERT INTO entity_facts (id, company_id, entity_kind, entity_key, entity_name, fact_key,
			value_num, unit, source, job, job_version, valid_from)
			VALUES ($1, $2, 'tenant', 'tenant', 'x', 'receivables_total', 1, 'inr', 'computed', 'j', 'v', '2026-01-01')`,
			uuid.Must(uuid.NewV7()), a.cid)
		return errors.New("rollback")
	})
	if insErr == nil || !strings.Contains(insErr.Error(), "row-level security") {
		t.Errorf("a cross-tenant insert must be refused by RLS WITH CHECK; got %v", insErr)
	}
}

// TestConcurrentRefreshesSerialise: two refreshes at once both succeed and
// leave exactly one current set — the advisory lock, not luck. NON-VACUOUS:
// remove the pg_advisory_xact_lock line and one of the two fails on the
// one-current unique index (or both commit and the census doubles).
func TestConcurrentRefreshesSerialise(t *testing.T) {
	b := newBook(t, "Profile Concurrency")
	b.seed(t)
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = profile.Refresh(b.ctx, b.pool, b.cid, profile.Options{AsOf: asOf})
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("refresh %d: %v", i, err)
		}
	}
	var current, cards int
	if err := b.pool.RunInReadOnlyTx(b.ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM entity_facts WHERE superseded_at IS NULL AND valid_to IS NULL AND fact_key = 'receivables_total'`).Scan(&current); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*) FROM tenant_profile WHERE superseded_at IS NULL`).Scan(&cards)
	}); err != nil {
		t.Fatal(err)
	}
	if current != 1 || cards != 1 {
		t.Errorf("after 4 concurrent refreshes: %d current receivables_total rows, %d current cards; want 1 and 1", current, cards)
	}
}

// TestOperatorNotesAreVersionedAndBounded: the user-editable half.
func TestOperatorNotesAreVersionedAndBounded(t *testing.T) {
	b := newBook(t, "Profile Notes")
	b.seed(t)
	if _, err := profile.Refresh(b.ctx, b.pool, b.cid, profile.Options{AsOf: asOf}); err != nil {
		t.Fatal(err)
	}
	card, err := profile.SetUserNotes(b.ctx, b.pool, b.cid, "We are a hardware wholesaler; Kaveri is family, never chase before 45 days.", "owner@example")
	if err != nil {
		t.Fatal(err)
	}
	if card.Version != 2 || !strings.Contains(card.Text, "<<OPERATOR NOTES") || card.EditedBy != "owner@example" {
		t.Errorf("edited card: %+v", card)
	}
	// A later refresh carries the notes forward.
	if _, err := profile.Refresh(b.ctx, b.pool, b.cid, profile.Options{AsOf: asOf}); err != nil {
		t.Fatal(err)
	}
	_, c3, err := profile.GetTenantProfile(b.ctx, b.pool, "")
	if err != nil {
		t.Fatal(err)
	}
	if c3.Version != 3 || !strings.Contains(c3.UserNotes, "never chase before 45 days") {
		t.Errorf("notes must survive a refresh: v%d notes=%q", c3.Version, c3.UserNotes)
	}
	if _, err := profile.SetUserNotes(b.ctx, b.pool, b.cid, strings.Repeat("x", profile.MaxUserNotes+1), "o"); !errors.Is(err, profile.ErrNotesTooLong) {
		t.Errorf("over-long notes: %v", err)
	}
	// 2000 non-ASCII runes estimate 2000 tokens — over the card cap: refused
	// by name, not truncated.
	if _, err := profile.SetUserNotes(b.ctx, b.pool, b.cid, strings.Repeat("क", profile.MaxUserNotes), "o"); !errors.Is(err, profile.ErrProfileTooLong) {
		t.Errorf("notes that blow the token cap must be refused: %v", err)
	}
}
