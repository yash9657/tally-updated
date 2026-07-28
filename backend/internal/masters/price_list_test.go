package masters_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/masters"
	"github.com/khata/cored/internal/platform/db"
)

// dec is a shorthand for the many literal rates and quantities below. Money and
// quantity are decimal.Decimal throughout — a float64 rate of 0.1 is not 0.1,
// and a price list is the one master whose numbers are multiplied by quantities
// and printed on a customer's invoice.
func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// TestPriceListLookupIsDeterministicForAnyItemAndDate is the central property of
// this master: for every day in the year there is EXACTLY ONE applicable rate,
// and which one it is does not depend on insertion order or on row ids.
//
// It is proved two ways, because the property has two halves:
//   - the POSITIVE half: two ADJACENT half-open windows are accepted, and every
//     single day in the combined span — including the day they meet — resolves
//     to one specific rate.
//   - the NEGATIVE half: a list that genuinely OVERLAPS an existing one in the
//     same scope is REFUSED outright. There is no tie-break rule to test,
//     because there is no tie-break rule: the second list never exists.
func TestPriceListLookupIsDeterministicForAnyItemAndDate(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	if _, err := m.CreateStockItem(ctx, masters.StockItem{Name: "Steel Rod 12mm"}, "tester"); err != nil {
		t.Fatalf("create stock item: %v", err)
	}
	item, err := m.StockItemID(ctx, "Steel Rod 12mm")
	if err != nil {
		t.Fatalf("resolve stock item: %v", err)
	}

	// Two ADJACENT windows: [Apr 1, Jul 1) and [Jul 1, Oct 1). Adjacent, not
	// overlapping — which is only true because the upper bound is exclusive.
	if _, err := m.CreatePriceList(ctx, masters.PriceList{
		Name: "Q1 Trade Rates", EffectiveFrom: "2025-04-01", EffectiveTo: "2025-07-01",
		Entries: []masters.PriceListEntry{{
			StockItemName: "Steel Rod 12mm", QtyToUnbounded: true, Rate: dec("100.00"),
		}},
	}, "tester"); err != nil {
		t.Fatalf("create Q1 list: %v", err)
	}
	if _, err := m.CreatePriceList(ctx, masters.PriceList{
		Name: "Q2 Trade Rates", EffectiveFrom: "2025-07-01", EffectiveTo: "2025-10-01",
		Entries: []masters.PriceListEntry{{
			StockItemName: "Steel Rod 12mm", QtyToUnbounded: true, Rate: dec("110.00"),
		}},
	}, "tester"); err != nil {
		t.Fatalf("a list starting exactly where the previous one ends must be accepted — "+
			"the windows are half-open [from, to) and so do not overlap: %v", err)
	}

	// Every day of the combined span resolves, and resolves to exactly the rate
	// the window it falls in states. The sweep is the point: a single spot check
	// would not notice a boundary that is off by one day.
	start := time.Date(2025, 4, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2025, 9, 30, 0, 0, 0, 0, time.UTC)
	boundary := time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC)
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		on := d.Format("2006-01-02")
		want := dec("100.00")
		wantList := "Q1 Trade Rates"
		if !d.Before(boundary) {
			want = dec("110.00")
			wantList = "Q2 Trade Rates"
		}
		got, err := m.ResolveRate(ctx, masters.PriceQuery{
			StockItemID: item, On: on, Qty: dec("1"),
		})
		if err != nil {
			t.Fatalf("no rate resolved for %s: %v — every day of a covered span must "+
				"price, or an invoice dated that day cannot be raised", on, err)
		}
		if !got.Rate.Equal(want) || got.PriceListName != wantList {
			t.Fatalf("on %s the rate resolved to %s from %q, want %s from %q — the window "+
				"boundary is off, so orders around it are invoiced at the wrong season's price",
				on, got.Rate.String(), got.PriceListName, want.String(), wantList)
		}
	}

	// The day the two windows meet belongs to the LATER one and only the later
	// one. Spelt out separately from the sweep so a failure names the boundary.
	if got, err := m.ResolveRate(ctx, masters.PriceQuery{
		StockItemID: item, On: "2025-07-01", Qty: dec("1"),
	}); err != nil || !got.Rate.Equal(dec("110.00")) {
		t.Fatalf("on the changeover day 2025-07-01 the rate resolved to %v (err %v), want "+
			"110.00 from the NEW list — the outgoing list's to-date is exclusive", got.Rate, err)
	}

	// The negative half: a genuinely overlapping list in the same scope is
	// refused by the database, so the ambiguity can never exist to be resolved.
	if _, err := m.CreatePriceList(ctx, masters.PriceList{
		Name: "Mid-Season Rates", EffectiveFrom: "2025-06-01", EffectiveTo: "2025-08-01",
		Entries: []masters.PriceListEntry{{
			StockItemName: "Steel Rod 12mm", QtyToUnbounded: true, Rate: dec("105.00"),
		}},
	}, "tester"); err == nil {
		t.Fatalf("a price list overlapping an existing one in the same scope was ACCEPTED — " +
			"two lists now claim the same customers on the same days, and which rate an " +
			"invoice carries depends on the order the planner returns rows in")
	}

	// ...and the overlapping list left nothing behind, so the books still price
	// exactly as they did before the attempt.
	if got, err := m.ResolveRate(ctx, masters.PriceQuery{
		StockItemID: item, On: "2025-06-15", Qty: dec("1"),
	}); err != nil || !got.Rate.Equal(dec("100.00")) {
		t.Fatalf("after the refused overlapping list, 2025-06-15 resolves to %v (err %v), "+
			"want 100.00 — the refused list left partial rows behind", got.Rate, err)
	}

	// A party-group-scoped list is a DIFFERENT scope, so it coexists with the
	// company-wide ones, and it wins for that group. That precedence cannot tie:
	// there are exactly two levels and at most one list at each.
	if _, err := m.CreatePriceList(ctx, masters.PriceList{
		Name: "Wholesale Rates", PartyGroupName: "Sundry Debtors",
		EffectiveFrom: "2025-04-01",
		Entries: []masters.PriceListEntry{{
			StockItemName: "Steel Rod 12mm", QtyToUnbounded: true, Rate: dec("90.00"),
		}},
	}, "tester"); err != nil {
		t.Fatalf("a party-group list overlapping the company-wide one in DATE but not in "+
			"scope must be accepted — narrowing by party group is the whole point: %v", err)
	}
	debtors, err := m.GroupID(ctx, "Sundry Debtors")
	if err != nil {
		t.Fatalf("resolve Sundry Debtors: %v", err)
	}
	scoped, err := m.ResolveRate(ctx, masters.PriceQuery{
		StockItemID: item, PartyGroupID: debtors, On: "2025-06-15", Qty: dec("1"),
	})
	if err != nil {
		t.Fatalf("resolve rate for a party group: %v", err)
	}
	if !scoped.Rate.Equal(dec("90.00")) || !scoped.PartyScoped {
		t.Fatalf("a party in Sundry Debtors was quoted %s (party-scoped=%v), want 90.00 from "+
			"the group's own list — the group list exists precisely to override the general one",
			scoped.Rate.String(), scoped.PartyScoped)
	}
	// A party in some OTHER group still gets the company-wide rate, so the
	// precedence above is a genuine override and not "the group list always wins".
	creditors, err := m.GroupID(ctx, "Sundry Creditors")
	if err != nil {
		t.Fatalf("resolve Sundry Creditors: %v", err)
	}
	other, err := m.ResolveRate(ctx, masters.PriceQuery{
		StockItemID: item, PartyGroupID: creditors, On: "2025-06-15", Qty: dec("1"),
	})
	if err != nil {
		t.Fatalf("resolve rate for an unpriced party group: %v", err)
	}
	if !other.Rate.Equal(dec("100.00")) || other.PartyScoped {
		t.Fatalf("a party outside the wholesale group was quoted %s (party-scoped=%v), want "+
			"100.00 from the company-wide list", other.Rate.String(), other.PartyScoped)
	}
}

// TestQuantitySlabsAreHalfOpen pins the [from, to) discipline for quantity
// breaks. A quantity sitting exactly on a boundary must land in EXACTLY ONE
// slab; an inclusive upper bound would put it in two, and the order would be
// priced at whichever row came back first — a wrong invoice with no error
// raised anywhere.
func TestQuantitySlabsAreHalfOpen(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	if _, err := m.CreateStockItem(ctx, masters.StockItem{Name: "Hex Bolt M8"}, "tester"); err != nil {
		t.Fatalf("create stock item: %v", err)
	}
	item, err := m.StockItemID(ctx, "Hex Bolt M8")
	if err != nil {
		t.Fatalf("resolve stock item: %v", err)
	}

	listID, err := m.CreatePriceList(ctx, masters.PriceList{
		Name: "Bolt Slabs", EffectiveFrom: "2025-04-01",
		Entries: []masters.PriceListEntry{
			{StockItemName: "Hex Bolt M8", QtyFrom: dec("0"), QtyTo: dec("100"), Rate: dec("50.00")},
			{StockItemName: "Hex Bolt M8", QtyFrom: dec("100"), QtyTo: dec("500"), Rate: dec("45.00")},
			{StockItemName: "Hex Bolt M8", QtyFrom: dec("500"), QtyToUnbounded: true, Rate: dec("40.00")},
		},
	}, "tester")
	if err != nil {
		t.Fatalf("adjacent half-open slabs must be accepted — [0,100) and [100,500) do not "+
			"overlap: %v", err)
	}

	// The boundary quantities are the whole test. 100 belongs to [100,500) and
	// NOT to [0,100); 500 belongs to [500,) and NOT to [100,500).
	for _, tc := range []struct {
		qty  string
		rate string
		why  string
	}{
		{"0", "50.00", "the bottom of the first slab is inclusive"},
		{"99.9999", "50.00", "just under the first boundary"},
		{"100", "45.00", "EXACTLY on the first boundary: it belongs to the slab that starts there"},
		{"100.0001", "45.00", "just over the first boundary"},
		{"499.9999", "45.00", "just under the second boundary"},
		{"500", "40.00", "EXACTLY on the second boundary"},
		{"10000", "40.00", "the top slab is unbounded above"},
	} {
		got, err := m.ResolveRate(ctx, masters.PriceQuery{
			StockItemID: item, On: "2025-06-01", Qty: dec(tc.qty),
		})
		if err != nil {
			t.Fatalf("quantity %s resolved no rate (%s): %v — a covered quantity that prices "+
				"nothing blocks the order entirely", tc.qty, tc.why, err)
		}
		if !got.Rate.Equal(dec(tc.rate)) {
			t.Fatalf("quantity %s priced at %s, want %s (%s) — the slab boundary is off by "+
				"one and the order is invoiced at the wrong break",
				tc.qty, got.Rate.String(), tc.rate, tc.why)
		}
	}

	// Counted straight from the table, so the "exactly one" is a property of the
	// stored ranges and not of the resolver's ORDER BY.
	for _, qty := range []string{"0", "100", "500"} {
		var matches int
		if err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx,
				`SELECT count(*) FROM price_list_entry
				  WHERE price_list_id = $1 AND stock_item_id = $2 AND qty_range @> $3::numeric`,
				listID, item, dec(qty)).Scan(&matches)
		}); err != nil {
			t.Fatalf("count matching slabs: %v", err)
		}
		if matches != 1 {
			t.Fatalf("quantity %s is contained by %d slabs, want exactly 1 — with two, the "+
				"rate charged depends on which row the planner returns first; with none, "+
				"the order cannot be priced at all", qty, matches)
		}
	}

	// The reported slab bounds round-trip, so a caller can show the customer
	// which break they fell into.
	got, err := m.ResolveRate(ctx, masters.PriceQuery{
		StockItemID: item, On: "2025-06-01", Qty: dec("100"),
	})
	if err != nil {
		t.Fatalf("resolve boundary quantity: %v", err)
	}
	if !got.QtyFrom.Equal(dec("100")) || got.QtyToUnbounded || !got.QtyTo.Equal(dec("500")) {
		t.Fatalf("the quoted slab reads [%s, %s) unbounded=%v, want [100, 500) — the customer "+
			"would be shown a break they are not in",
			got.QtyFrom.String(), got.QtyTo.String(), got.QtyToUnbounded)
	}
	topSlab, err := m.ResolveRate(ctx, masters.PriceQuery{
		StockItemID: item, On: "2025-06-01", Qty: dec("900"),
	})
	if err != nil {
		t.Fatalf("resolve top slab: %v", err)
	}
	if !topSlab.QtyToUnbounded {
		t.Fatalf("the top slab reads back with an upper bound of %s — an unbounded slab "+
			"reported as bounded at zero would look like an empty break", topSlab.QtyTo.String())
	}

	// Genuinely OVERLAPPING slabs (not merely touching) are refused by the
	// database, so the ambiguity cannot be stored in the first place.
	if _, err := m.CreatePriceList(ctx, masters.PriceList{
		Name: "Overlapping Slabs", EffectiveFrom: "2026-04-01",
		Entries: []masters.PriceListEntry{
			{StockItemName: "Hex Bolt M8", QtyFrom: dec("0"), QtyTo: dec("100"), Rate: dec("50.00")},
			{StockItemName: "Hex Bolt M8", QtyFrom: dec("50"), QtyTo: dec("200"), Rate: dec("45.00")},
		},
	}, "tester"); err == nil {
		t.Fatalf("overlapping quantity slabs [0,100) and [50,200) were ACCEPTED — any order " +
			"between 50 and 100 now has two rates and no rule says which one applies")
	}

	// And a slab whose bounds are equal — [100,100), which contains nothing — is
	// refused at the door with a message naming the bounds, rather than sitting
	// in the master as a rate that can never be charged.
	_, err = m.CreatePriceList(ctx, masters.PriceList{
		Name: "Empty Slab", EffectiveFrom: "2027-04-01",
		Entries: []masters.PriceListEntry{
			{StockItemName: "Hex Bolt M8", QtyFrom: dec("100"), QtyTo: dec("100"), Rate: dec("50.00")},
		},
	}, "tester")
	if err == nil {
		t.Fatalf("a slab [100, 100) containing no quantity was accepted")
	}
	if !strings.Contains(err.Error(), "contains no quantity") {
		t.Fatalf("an empty slab was refused with %q — the message must name the bounds the "+
			"user typed, not a constraint name", err)
	}
}

// TestPriceListIsTenantScoped pins that a rate never crosses a company
// boundary, both ways: the resolver cannot READ another tenant's list, and the
// database itself refuses a WRITE that points at another tenant's master.
//
// The write half matters independently of RLS. RLS hides rows from a SELECT,
// but a plain single-column FK would still accept a stock item id belonging to
// another company if one leaked in through a request body — and the entry would
// then be invisible to both tenants while quietly pricing goods.
func TestPriceListIsTenantScoped(t *testing.T) {
	pool, ctxA, companyA := setup(t)
	ctxB := secondCompany(t, pool)
	m := masters.NewService(pool)

	for _, c := range []struct {
		ctx  context.Context
		rate string
		name string
	}{{ctxA, "100.00", "House Rates"}, {ctxB, "777.00", "House Rates"}} {
		if _, err := m.CreateStockItem(c.ctx, masters.StockItem{Name: "Shared Widget"}, "tester"); err != nil {
			t.Fatalf("create stock item: %v", err)
		}
		if _, err := m.CreatePriceList(c.ctx, masters.PriceList{
			Name: c.name, EffectiveFrom: "2025-04-01",
			Entries: []masters.PriceListEntry{{
				StockItemName: "Shared Widget", QtyToUnbounded: true, Rate: dec(c.rate),
			}},
		}, "tester"); err != nil {
			t.Fatalf("create price list: %v", err)
		}
	}

	itemA, err := m.StockItemID(ctxA, "Shared Widget")
	if err != nil {
		t.Fatalf("resolve item in A: %v", err)
	}
	itemB, err := m.StockItemID(ctxB, "Shared Widget")
	if err != nil {
		t.Fatalf("resolve item in B: %v", err)
	}
	if itemA == itemB {
		t.Fatalf("both companies resolved 'Shared Widget' to %s — the tenants are not "+
			"separated at all", itemA)
	}

	quoteA, err := m.ResolveRate(ctxA, masters.PriceQuery{
		StockItemID: itemA, On: "2025-06-01", Qty: dec("1"),
	})
	if err != nil {
		t.Fatalf("resolve rate in A: %v", err)
	}
	if !quoteA.Rate.Equal(dec("100.00")) {
		t.Fatalf("company A was quoted %s, want its own 100.00 — B's price list is leaking "+
			"into A's invoices", quoteA.Rate.String())
	}
	quoteB, err := m.ResolveRate(ctxB, masters.PriceQuery{
		StockItemID: itemB, On: "2025-06-01", Qty: dec("1"),
	})
	if err != nil {
		t.Fatalf("resolve rate in B: %v", err)
	}
	if !quoteB.Rate.Equal(dec("777.00")) {
		t.Fatalf("company B was quoted %s, want its own 777.00", quoteB.Rate.String())
	}

	// Asking company A for B's item id must answer "no rate", not B's rate.
	if _, err := m.ResolveRate(ctxA, masters.PriceQuery{
		StockItemID: itemB, On: "2025-06-01", Qty: dec("1"),
	}); !errors.Is(err, masters.ErrNoRate) {
		t.Fatalf("company A resolved a rate for company B's stock item (err %v), want "+
			"ErrNoRate — that is one tenant reading another's commercial terms", err)
	}

	// The FK half. A raw insert in A's session, correctly stamped with A's
	// company_id (so RLS is satisfied) but pointing at B's stock item, must be
	// refused by price_list_entry_item_fk itself.
	listA, err := m.PriceListID(ctxA, "House Rates")
	if err != nil {
		t.Fatalf("resolve A's price list: %v", err)
	}
	entryID, _ := uuid.NewV7()
	err = pool.RunInTx(ctxA, func(ctx context.Context, tx pgx.Tx) error {
		_, execErr := tx.Exec(ctx,
			`INSERT INTO price_list_entry
			   (id, company_id, price_list_id, stock_item_id, qty_range, rate)
			 VALUES ($1, $2, $3, $4, numrange(0, NULL, '[)'), 1)`,
			entryID, companyA, listA, itemB)
		return execErr
	})
	if err == nil {
		t.Fatalf("company A stored a price for company B's stock item — the entry is now " +
			"invisible to both tenants and prices goods neither of them can see")
	}
	if !strings.Contains(err.Error(), "price_list_entry_item_fk") {
		t.Fatalf("the cross-tenant entry was refused by %q, but it must be the composite FK "+
			"price_list_entry_item_fk — anything else means the refusal rests on RLS or on "+
			"the application, not on the database's own referential integrity", err)
	}

	// Same for the party group the list is narrowed to.
	groupB, err := m.GroupID(ctxB, "Sundry Debtors")
	if err != nil {
		t.Fatalf("resolve B's Sundry Debtors: %v", err)
	}
	listID, _ := uuid.NewV7()
	err = pool.RunInTx(ctxA, func(ctx context.Context, tx pgx.Tx) error {
		_, execErr := tx.Exec(ctx,
			`INSERT INTO price_list (id, company_id, name, party_group_id, effective)
			 VALUES ($1, $2, 'Cross Tenant Scope', $3, daterange('2030-04-01', NULL, '[)'))`,
			listID, companyA, groupB)
		return execErr
	})
	if err == nil {
		t.Fatalf("company A created a price list scoped to company B's party group — the " +
			"list would apply to nobody A can see")
	}
	if !strings.Contains(err.Error(), "price_list_party_group_fk") {
		t.Fatalf("the cross-tenant party group was refused by %q, want the composite FK "+
			"price_list_party_group_fk", err)
	}
}

// TestCreatePriceListAppendsMasterAudit pins the Rule 11(g) edit-log contract
// for this master, and pins it to the transaction: a REFUSED create must leave
// no trace. An edit log that records creations which never happened cannot
// answer "what does this company actually have", which is the only question an
// auditor asks of it.
func TestCreatePriceListAppendsMasterAudit(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	if _, err := m.CreateStockItem(ctx, masters.StockItem{Name: "Audited Item"}, "tester"); err != nil {
		t.Fatalf("create stock item: %v", err)
	}

	id, err := m.CreatePriceList(ctx, masters.PriceList{
		Name: "Audited Rates", PartyGroupName: "Sundry Debtors",
		EffectiveFrom: "2025-04-01", EffectiveTo: "2025-07-01",
		Entries: []masters.PriceListEntry{
			{StockItemName: "Audited Item", QtyFrom: dec("0"), QtyTo: dec("10"), Rate: dec("20.00")},
			{StockItemName: "Audited Item", QtyFrom: dec("10"), QtyToUnbounded: true,
				Rate: dec("18.00"), DiscountPct: dec("5.00")},
		},
	}, "tester")
	if err != nil {
		t.Fatalf("create price list: %v", err)
	}

	// Counted on its own, BEFORE anything is read out of the row: with zero rows
	// an aggregate read would fail on a NULL scan and report a scanning problem,
	// when the fact worth reporting is that the edit log lost the change.
	var rows int
	if err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM master_audit
			  WHERE entity_type = 'price_list' AND entity_id = $1`, id).Scan(&rows)
	}); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if rows != 1 {
		t.Fatalf("creating one price list must append exactly one audit row, got %d — a "+
			"missing row loses the change entirely (the Rule 11(g) log would not show that "+
			"these prices were ever set), a duplicate double-counts it", rows)
	}

	var (
		op         string
		beforeNull bool
		afterName  string
		afterFrom  string
		afterCount string
		createdBy  string
	)
	if err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT op::text, before_doc IS NULL,
			        after_doc->>'name', after_doc->>'effective_from',
			        after_doc->>'entries', created_by
			   FROM master_audit
			  WHERE entity_type = 'price_list' AND entity_id = $1`, id).
			Scan(&op, &beforeNull, &afterName, &afterFrom, &afterCount, &createdBy)
	}); err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if op != "CREATE" {
		t.Fatalf("price list audit op is %q, want CREATE — the edit log would report the "+
			"wrong kind of change", op)
	}
	if !beforeNull {
		t.Fatalf("a CREATE has no before state, but before_doc was not NULL — the log would " +
			"claim the price list existed beforehand")
	}
	if afterName != "Audited Rates" || afterFrom != "2025-04-01" || afterCount != "2" {
		t.Fatalf("audit after_doc reads name=%q from=%q entries=%q, want "+
			"\"Audited Rates\"/\"2025-04-01\"/\"2" +
			"\" — the log cannot be matched to the rates it describes",
			afterName, afterFrom, afterCount)
	}
	if createdBy != "tester" {
		t.Fatalf("audit created_by is %q, want \"tester\" — an edit log that cannot name who "+
			"changed a price is not an edit log", createdBy)
	}

	// The rates themselves landed, and the discount round-trips into the quote.
	item, err := m.StockItemID(ctx, "Audited Item")
	if err != nil {
		t.Fatalf("resolve stock item: %v", err)
	}
	// The list is narrowed to Sundry Debtors, so the lookup must name that group
	// — a lookup without one sees only the company-wide lists, of which there
	// are none here.
	debtors, err := m.GroupID(ctx, "Sundry Debtors")
	if err != nil {
		t.Fatalf("resolve Sundry Debtors: %v", err)
	}
	quote, err := m.ResolveRate(ctx, masters.PriceQuery{
		StockItemID: item, PartyGroupID: debtors, On: "2025-05-01", Qty: dec("50"),
	})
	if err != nil {
		t.Fatalf("resolve rate: %v", err)
	}
	if !quote.Rate.Equal(dec("18.00")) || !quote.DiscountPct.Equal(dec("5.00")) ||
		!quote.NetRate.Equal(dec("17.10")) {
		t.Fatalf("quote reads rate=%s discount=%s net=%s, want 18.00 / 5.00 / 17.10 — the "+
			"gross rate and the discount must both survive, because the invoice prints both",
			quote.Rate.String(), quote.DiscountPct.String(), quote.NetRate.String())
	}

	// A duplicate NAME is refused by UNIQUE (company_id, name). Deliberately in
	// a window that does not overlap the first list, so it is the name — not the
	// overlap constraint — being tested. The audit append lives inside the same
	// transaction, so the rollback must take the audit row with it.
	if _, err := m.CreatePriceList(ctx, masters.PriceList{
		Name: "Audited Rates", EffectiveFrom: "2026-04-01",
		Entries: []masters.PriceListEntry{{
			StockItemName: "Audited Item", QtyToUnbounded: true, Rate: dec("25.00"),
		}},
	}, "tester"); err == nil {
		t.Fatalf("a second price list named 'Audited Rates' must be refused: two lists with " +
			"one name cannot be told apart on a voucher or in a report")
	}

	var total int
	if err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM master_audit WHERE entity_type = 'price_list'`).Scan(&total)
	}); err != nil {
		t.Fatalf("read audit after failed create: %v", err)
	}
	if total != 1 {
		t.Fatalf("after a REFUSED price list create the company has %d price_list audit rows, "+
			"want 1 — the edit log is claiming a list that does not exist", total)
	}
}

// TestRateOutsideEveryEffectiveWindowIsNotFound pins the one answer this
// resolver must never invent. A date before every window, a date on or after
// the last window's exclusive end, or a quantity below every slab must come
// back as a TYPED not-found.
//
// Returning a zero rate instead would be catastrophic and silent: zero is a
// legal price (free samples exist), so the caller cannot tell it apart from "no
// answer", and the result is an invoice that bills the goods for nothing and
// still balances to the paisa.
func TestRateOutsideEveryEffectiveWindowIsNotFound(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	if _, err := m.CreateStockItem(ctx, masters.StockItem{Name: "Seasonal Item"}, "tester"); err != nil {
		t.Fatalf("create stock item: %v", err)
	}
	item, err := m.StockItemID(ctx, "Seasonal Item")
	if err != nil {
		t.Fatalf("resolve stock item: %v", err)
	}
	listID, err := m.CreatePriceList(ctx, masters.PriceList{
		Name: "Season Rates", EffectiveFrom: "2025-04-01", EffectiveTo: "2025-07-01",
		Entries: []masters.PriceListEntry{{
			StockItemName: "Seasonal Item", QtyFrom: dec("10"), QtyTo: dec("100"),
			Rate: dec("60.00"),
		}},
	}, "tester")
	if err != nil {
		t.Fatalf("create price list: %v", err)
	}

	// Inside the window and inside the slab, there IS a rate — so the misses
	// below are genuine misses and not "nothing ever resolves".
	if quote, err := m.ResolveRate(ctx, masters.PriceQuery{
		StockItemID: item, On: "2025-06-30", Qty: dec("50"),
	}); err != nil || !quote.Rate.Equal(dec("60.00")) {
		t.Fatalf("inside the window 2025-06-30 @ qty 50 resolved %v (err %v), want 60.00",
			quote.Rate, err)
	}

	for _, tc := range []struct {
		on  string
		qty string
		why string
	}{
		{"2025-03-31", "50", "the day before the window opens"},
		{"2025-07-01", "50", "the window's to-date is EXCLUSIVE, so it is already closed"},
		{"2026-01-01", "50", "long after the window closed"},
		{"2025-06-01", "5", "below every quantity slab"},
		{"2025-06-01", "100", "exactly the slab's exclusive upper bound, so above every slab"},
		{"2025-06-01", "1000", "far above every quantity slab"},
	} {
		quote, err := m.ResolveRate(ctx, masters.PriceQuery{
			StockItemID: item, On: tc.on, Qty: dec(tc.qty),
		})
		if err == nil {
			t.Fatalf("%s @ qty %s (%s) returned a rate of %s with no error — an uncovered "+
				"lookup MUST fail loudly; a rate invented here goes straight onto an invoice",
				tc.on, tc.qty, tc.why, quote.Rate.String())
		}
		if !errors.Is(err, masters.ErrNoRate) {
			t.Fatalf("%s @ qty %s (%s) failed with %v, want ErrNoRate — the caller cannot "+
				"distinguish 'no rate applies' from a database fault", tc.on, tc.qty, tc.why, err)
		}
		if !quote.Rate.IsZero() || !quote.NetRate.IsZero() || quote.PriceListID != uuid.Nil {
			t.Fatalf("%s @ qty %s (%s) returned a populated quote %+v alongside its error — "+
				"a caller that checks the value before the error would invoice at it",
				tc.on, tc.qty, tc.why, quote)
		}
	}

	// ErrNoRate is deliberately NOT ErrNotFound: the masters all exist, the list
	// simply says nothing about this day. The remedies differ (create the master
	// vs. widen the window), so a caller must be able to tell them apart.
	_, err = m.ResolveRate(ctx, masters.PriceQuery{
		StockItemID: item, On: "2025-03-31", Qty: dec("50"),
	})
	if errors.Is(err, masters.ErrNotFound) {
		t.Fatalf("an uncovered date reported ErrNotFound (%v) — that tells the user to create "+
			"a master that already exists, instead of to widen the window", err)
	}

	// A quantity of exactly the slab's lower bound IS covered, which pins that
	// the misses above come from the exclusive UPPER bound and not from the slab
	// being unreachable in both directions.
	if quote, err := m.ResolveRate(ctx, masters.PriceQuery{
		StockItemID: item, On: "2025-06-01", Qty: dec("10"),
	}); err != nil || !quote.Rate.Equal(dec("60.00")) {
		t.Fatalf("quantity exactly 10, the slab's INCLUSIVE lower bound, resolved %v (err %v), "+
			"want 60.00", quote.Rate, err)
	}

	// The list is genuinely there and genuinely bounded — read back through the
	// header so an open-ended list is not confused with a closed one.
	info, err := m.PriceListInfo(ctx, listID)
	if err != nil {
		t.Fatalf("read price list header: %v", err)
	}
	if info.EffectiveFrom != "2025-04-01" || info.EffectiveTo != "2025-07-01" {
		t.Fatalf("price list window reads back [%s, %s), want [2025-04-01, 2025-07-01) — the "+
			"stored window is not the one the user typed", info.EffectiveFrom, info.EffectiveTo)
	}
	if _, ok := db.CompanyFromContext(ctx); !ok {
		t.Fatalf("test context lost its company scope")
	}
}
