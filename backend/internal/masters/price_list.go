package masters

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/platform/db"
)

// --------------------------------------------------------------------------
// Price lists
// --------------------------------------------------------------------------

// halfOpenBounds is the range-bound spelling used for BOTH a price list's
// effective window and an entry's quantity slab: lower inclusive, upper
// EXCLUSIVE.
//
// It is one constant rather than two literals because it is one rule, and the
// rule is the whole reason this master can be looked up deterministically: a
// value exactly on a boundary belongs to the slab that STARTS there and to no
// other. Slabs [0,100) and [100,) put a quantity of exactly 100 in the second
// and only the second; lists [Apr 1, Jul 1) and [Jul 1, Oct 1) put Jul 1 in the
// second and only the second. Spelt '[]' instead, a quantity of 100 would match
// two slabs and the order would be priced at whichever row came back first —
// a wrong invoice with no error raised anywhere. Migration 0030's
// price_list_entry_qty_half_open CHECK refuses that shape at the database, so
// this constant and the constraint agree or nothing is written at all.
const halfOpenBounds = "[)"

// ErrNoRate is returned by ResolveRate when NO price list rate applies to the
// asked-for (item, party group, date, quantity).
//
// It is a distinct sentinel from ErrNotFound on purpose. ErrNotFound means "the
// master you named does not exist" and the remedy is to create it; ErrNoRate
// means every master involved exists and simply says nothing about this date or
// this quantity, and the remedy is to widen a window or add a slab. Collapsing
// the two would leave the caller unable to tell a typo from a gap.
//
// What ResolveRate must NEVER do is return a zero rate here. A zero rate is a
// perfectly valid price — free samples are real — so a caller cannot tell it
// apart from "no answer", and the failure mode is an invoice that bills the
// goods for nothing and balances perfectly while doing it.
var ErrNoRate = errors.New("masters: no price list rate applies")

// ErrAmbiguousRate is returned when more than one price list of the SAME
// specificity offers a rate for the same (item, date, quantity).
//
// Migration 0030's two exclusion constraints make this unreachable: at most one
// list is in force per (company, party-group scope) on any date, and within a
// list at most one slab contains any quantity. The check exists anyway because
// the day somebody drops a constraint to "fix" a data-migration, the failure
// mode without it is silent — an arbitrary one of the two rates, chosen by the
// planner, on real invoices. Loud beats arbitrary.
var ErrAmbiguousRate = errors.New("masters: more than one price list rate applies")

// PriceList is a named set of rates for stock items, in force over a half-open
// date window, optionally narrowed to a party group.
//
// EffectiveTo is EXCLUSIVE and may be empty for an open-ended list ("these are
// our rates until we say otherwise"), which is the ordinary case. Two lists
// covering the same day in the same scope are REFUSED by the database rather
// than resolved by a precedence rule — see migration 0030's header for why:
// every available tie-break silently prices some orders at last season's rate
// and tells nobody.
//
// PartyGroupName narrows the list to one party group; empty means it applies to
// every party. A group-scoped list BEATS the company-wide one — the only
// precedence anywhere in this design, and it cannot tie because there are only
// two levels.
type PriceList struct {
	ID             uuid.UUID
	Name           string
	PartyGroupName string // "" = applies to every party
	EffectiveFrom  string // ISO date, required; INCLUSIVE
	EffectiveTo    string // ISO date, "" = open-ended; EXCLUSIVE
	Entries        []PriceListEntry
}

// PriceListEntry is one item's rate within one list, for one quantity slab.
//
// QtyFrom is INCLUSIVE and QtyTo is EXCLUSIVE. QtyToUnbounded marks the top
// slab ("and everything above") and is REQUIRED for it — there is deliberately
// no "zero means unbounded" shorthand, because zero is a real bound elsewhere
// in this struct (QtyFrom: 0 is the ordinary first slab) and a shorthand that
// means "nothing" in one field and "the bottom" in the next is how an
// off-by-one gets written.
//
// Rate and DiscountPct are kept apart rather than pre-netted because the
// invoice has to print both: "1,000.00 less 5%" is a different document from
// "950.00", and a customer who negotiated a trade discount will ask where it
// went. ResolveRate returns the net alongside them.
type PriceListEntry struct {
	StockItemName  string
	QtyFrom        decimal.Decimal // inclusive
	QtyTo          decimal.Decimal // EXCLUSIVE; ignored when QtyToUnbounded
	QtyToUnbounded bool
	Rate           decimal.Decimal
	DiscountPct    decimal.Decimal
}

// PriceQuery is what the voucher path will ask: what do we charge for this item,
// to this party, on this date, at this quantity.
type PriceQuery struct {
	StockItemID uuid.UUID
	// PartyGroupID narrows the lookup to a party group. uuid.Nil means the
	// caller has no party in hand (or the party's group carries no list) and
	// only the company-wide list is considered.
	//
	// The group is matched EXACTLY; the account_group tree is deliberately NOT
	// walked. Walking it would make "which list applied" depend on the shape of
	// somebody's chart of accounts — re-parenting a debtor group would silently
	// re-price them — and would reintroduce the ties this design removed.
	PartyGroupID uuid.UUID
	On           string // ISO date the rate is wanted for
	Qty          decimal.Decimal
}

// PriceQuote is the single, deterministic answer to a PriceQuery.
type PriceQuote struct {
	PriceListID   uuid.UUID
	PriceListName string
	// PartyScoped reports which precedence level answered: true = a
	// party-group list, false = the company-wide list. The voucher path will
	// want this on the audit trail, because "why was this customer charged
	// less" is a question somebody eventually asks.
	PartyScoped bool
	Rate        decimal.Decimal
	DiscountPct decimal.Decimal
	// NetRate is Rate less DiscountPct, rounded to 4 places — the scale of
	// stock_movement.rate, so the number the voucher line stores is the number
	// quoted here and not a re-rounding of it.
	NetRate        decimal.Decimal
	QtyFrom        decimal.Decimal
	QtyTo          decimal.Decimal
	QtyToUnbounded bool
}

// CreatePriceList creates a price list with its rate entries and appends a
// master_audit CREATE row.
//
// Header, entries and audit row all land in ONE transaction. A list whose
// header committed but whose entries did not is a master that exists, shows in
// every list of price lists, and prices nothing — and because the overlap
// constraint counts it, it also blocks the user from entering the list again.
func (s *Service) CreatePriceList(ctx context.Context, pl PriceList, createdBy string) (uuid.UUID, error) {
	if pl.Name == "" {
		return uuid.Nil, errors.New("masters: price list name is required")
	}
	if pl.EffectiveFrom == "" {
		return uuid.Nil, fmt.Errorf(
			"masters: price list %q needs a date it takes effect from: a list with no start "+
				"date would price back-dated vouchers from a season that had not happened yet",
			pl.Name)
	}
	from, err := parseOptionalDate(pl.EffectiveFrom, "effective-from date")
	if err != nil {
		return uuid.Nil, err
	}
	to, err := parseOptionalDate(pl.EffectiveTo, "effective-to date")
	if err != nil {
		return uuid.Nil, err
	}
	// The upper bound is EXCLUSIVE, so "to" equal to "from" is an empty window:
	// the list would exist, appear in every master list, and never once apply.
	// Caught here so the message names the two dates the user typed instead of
	// surfacing price_list_effective_nonempty.
	if to != nil && !to.After(*from) {
		return uuid.Nil, fmt.Errorf(
			"masters: price list %q is effective from %s to %s, an empty window "+
				"(the to-date is exclusive, so it must be strictly later) — the list would never apply",
			pl.Name, pl.EffectiveFrom, pl.EffectiveTo)
	}
	if len(pl.Entries) == 0 {
		return uuid.Nil, fmt.Errorf(
			"masters: price list %q carries no rates — an empty list prices nothing and "+
				"still blocks another list from covering those dates", pl.Name)
	}
	for i, e := range pl.Entries {
		if e.StockItemName == "" {
			return uuid.Nil, fmt.Errorf("masters: price list %q entry %d names no stock item", pl.Name, i+1)
		}
		if e.Rate.IsNegative() {
			return uuid.Nil, fmt.Errorf(
				"masters: price list %q rate for %q is negative (%s)",
				pl.Name, e.StockItemName, e.Rate.StringFixed(4))
		}
		if e.DiscountPct.IsNegative() || e.DiscountPct.GreaterThan(decimal.NewFromInt(100)) {
			return uuid.Nil, fmt.Errorf(
				"masters: price list %q discount for %q is %s%%, outside 0–100",
				pl.Name, e.StockItemName, e.DiscountPct.String())
		}
		if e.QtyFrom.IsNegative() {
			return uuid.Nil, fmt.Errorf(
				"masters: price list %q slab for %q starts at a negative quantity (%s)",
				pl.Name, e.StockItemName, e.QtyFrom.String())
		}
		// QtyTo is EXCLUSIVE: equal bounds make a slab that contains nothing,
		// which sits in the master looking like a rate somebody will be charged
		// and never is.
		if !e.QtyToUnbounded && !e.QtyTo.GreaterThan(e.QtyFrom) {
			return uuid.Nil, fmt.Errorf(
				"masters: price list %q slab for %q is [%s, %s) which contains no quantity — "+
					"the upper bound is exclusive, so set QtyToUnbounded for the top slab",
				pl.Name, e.StockItemName, e.QtyFrom.String(), e.QtyTo.String())
		}
	}

	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return uuid.Nil, db.ErrNoCompany
	}
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("masters: new price list id: %w", err)
	}

	err = s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var partyGroupID *uuid.UUID
		if pl.PartyGroupName != "" {
			var gid uuid.UUID
			err := tx.QueryRow(ctx,
				`SELECT id FROM account_group WHERE name = $1`, pl.PartyGroupName).Scan(&gid)
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("masters: %w: party group %q", ErrNotFound, pl.PartyGroupName)
			}
			if err != nil {
				return fmt.Errorf("masters: resolve party group: %w", err)
			}
			partyGroupID = &gid
		}

		if _, err := tx.Exec(ctx,
			`INSERT INTO price_list (id, company_id, name, party_group_id, effective)
			 VALUES ($1, $2, $3, $4,
			         daterange($5::date, NULLIF($6,'')::date, $7))`,
			id, companyID, pl.Name, partyGroupID,
			pl.EffectiveFrom, pl.EffectiveTo, halfOpenBounds,
		); err != nil {
			return fmt.Errorf("masters: insert price list: %w", err)
		}

		for i, e := range pl.Entries {
			var itemID uuid.UUID
			err := tx.QueryRow(ctx,
				`SELECT id FROM stock_item WHERE name = $1`, e.StockItemName).Scan(&itemID)
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("masters: %w: stock item %q", ErrNotFound, e.StockItemName)
			}
			if err != nil {
				return fmt.Errorf("masters: resolve stock item %q: %w", e.StockItemName, err)
			}
			entryID, err := uuid.NewV7()
			if err != nil {
				return fmt.Errorf("masters: new price list entry id: %w", err)
			}
			var upper *decimal.Decimal
			if !e.QtyToUnbounded {
				qtyTo := e.QtyTo
				upper = &qtyTo
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO price_list_entry
				   (id, company_id, price_list_id, stock_item_id, qty_range, rate, discount_pct)
				 VALUES ($1, $2, $3, $4,
				         numrange($5::numeric, $6::numeric, $7), $8, $9)`,
				entryID, companyID, id, itemID,
				e.QtyFrom, upper, halfOpenBounds, e.Rate, e.DiscountPct,
			); err != nil {
				return fmt.Errorf("masters: insert price list %q entry %d (%s): %w",
					pl.Name, i+1, e.StockItemName, err)
			}
		}

		after := map[string]any{
			"name":           pl.Name,
			"party_group":    pl.PartyGroupName,
			"effective_from": pl.EffectiveFrom,
			"effective_to":   pl.EffectiveTo,
			"entries":        len(pl.Entries),
		}
		return appendAudit(ctx, tx, companyID, "price_list", id, OpCreate, nil, after, createdBy)
	})
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// PriceListID resolves a price list's id by name within the company in context.
func (s *Service) PriceListID(ctx context.Context, name string) (uuid.UUID, error) {
	return s.resolveID(ctx, "price_list", name)
}

// PriceListInfo reads a list's header back by id, with its window in the same
// optional ISO-string form CreatePriceList accepts: an open-ended list reads
// back with EffectiveTo == "", never "0001-01-01". Entries are not loaded.
//
// The dates are scanned through *time.Time for the same reason StockBatchInfo
// does it: a plain time.Time cannot express "unbounded", and the zero instant
// is a date a report will happily believe.
func (s *Service) PriceListInfo(ctx context.Context, priceListID uuid.UUID) (PriceList, error) {
	out := PriceList{ID: priceListID}
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var from, to *time.Time
		var groupName *string
		err := tx.QueryRow(ctx,
			`SELECT pl.name, g.name, lower(pl.effective), upper(pl.effective)
			   FROM price_list pl
			   LEFT JOIN account_group g
			     ON g.company_id = pl.company_id AND g.id = pl.party_group_id
			  WHERE pl.id = $1`,
			priceListID).Scan(&out.Name, &groupName, &from, &to)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("masters: %w: price list %s", ErrNotFound, priceListID)
		}
		if err != nil {
			return fmt.Errorf("masters: read price list: %w", err)
		}
		if groupName != nil {
			out.PartyGroupName = *groupName
		}
		out.EffectiveFrom = formatOptionalDate(from)
		out.EffectiveTo = formatOptionalDate(to)
		return nil
	})
	if err != nil {
		return PriceList{}, err
	}
	return out, nil
}

// ResolveRate answers a PriceQuery with exactly one rate, or a typed error.
//
// This is the function the voucher path will call once per inventory line. It
// does NOT write anything and it is NOT wired into posting in this slice.
//
// Determinism comes from the schema, not from an ORDER BY tie-break:
// price_list_no_overlap_in_scope means at most ONE list is in force per
// (company, party-group scope) on the asked-for date, and
// price_list_entry_slabs_disjoint means at most ONE slab of that list contains
// the asked-for quantity. So the query can return at most one row per
// specificity level — at most two rows in all — and the party-scoped one wins.
// If it ever sees two rows at the SAME level it refuses with ErrAmbiguousRate
// rather than picking one; see that sentinel's comment.
func (s *Service) ResolveRate(ctx context.Context, q PriceQuery) (PriceQuote, error) {
	if q.StockItemID == uuid.Nil {
		return PriceQuote{}, errors.New("masters: price lookup needs a stock item")
	}
	if q.On == "" {
		return PriceQuote{}, errors.New("masters: price lookup needs a date: a rate is only " +
			"meaningful as at a day, because a list has a window")
	}
	if _, err := parseOptionalDate(q.On, "price lookup date"); err != nil {
		return PriceQuote{}, err
	}
	if q.Qty.IsNegative() {
		return PriceQuote{}, fmt.Errorf("masters: price lookup quantity %s is negative", q.Qty.String())
	}

	var partyGroup *uuid.UUID
	if q.PartyGroupID != uuid.Nil {
		gid := q.PartyGroupID
		partyGroup = &gid
	}

	type candidate struct {
		listID    uuid.UUID
		listName  string
		scoped    bool
		rate      decimal.Decimal
		discount  decimal.Decimal
		qtyFrom   decimal.Decimal
		qtyTo     decimal.Decimal
		unbounded bool
	}
	var found []candidate

	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// `pl.party_group_id = $2` is NULL-safe by omission: when $2 is NULL the
		// comparison is UNKNOWN for every row, so only the company-wide arm
		// (IS NULL) can match — which is exactly the wanted behaviour for a
		// caller with no party in hand.
		//
		// The upper slab bound is read as (IS NULL, COALESCE(...)) rather than
		// scanned through a pointer so that "unbounded" is a boolean the caller
		// must look at, not a zero that reads as a legitimate bound of 0.
		rows, err := tx.Query(ctx,
			`SELECT pl.id, pl.name, (pl.party_group_id IS NOT NULL),
			        e.rate, e.discount_pct,
			        lower(e.qty_range),
			        (upper(e.qty_range) IS NULL),
			        COALESCE(upper(e.qty_range), 0)
			   FROM price_list_entry e
			   JOIN price_list pl
			     ON pl.company_id = e.company_id AND pl.id = e.price_list_id
			  WHERE e.stock_item_id = $1
			    AND (pl.party_group_id = $2 OR pl.party_group_id IS NULL)
			    AND pl.effective  @> $3::date
			    AND e.qty_range   @> $4::numeric
			  ORDER BY (pl.party_group_id IS NOT NULL) DESC`,
			q.StockItemID, partyGroup, q.On, q.Qty)
		if err != nil {
			return fmt.Errorf("masters: price lookup: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var c candidate
			if err := rows.Scan(&c.listID, &c.listName, &c.scoped, &c.rate, &c.discount,
				&c.qtyFrom, &c.unbounded, &c.qtyTo); err != nil {
				return fmt.Errorf("masters: scan price lookup: %w", err)
			}
			found = append(found, c)
		}
		return rows.Err()
	})
	if err != nil {
		return PriceQuote{}, err
	}

	if len(found) == 0 {
		// Typed, never a zero rate. A zero rate is a legal price (free
		// samples), so a caller cannot tell it apart from "no answer" — and the
		// consequence of getting that wrong is an invoice that bills the goods
		// for nothing and still balances.
		return PriceQuote{}, fmt.Errorf(
			"%w: stock item %s, quantity %s, on %s",
			ErrNoRate, q.StockItemID, q.Qty.String(), q.On)
	}

	// Rows arrive party-scoped first, so found[0] is the most specific answer.
	// Anything else at that SAME specificity is a genuine ambiguity.
	best := found[0]
	ties := 0
	for _, c := range found {
		if c.scoped == best.scoped {
			ties++
		}
	}
	if ties > 1 {
		return PriceQuote{}, fmt.Errorf(
			"%w: stock item %s on %s at quantity %s is covered by %d price lists of equal "+
				"scope (%s, %s) — no rate can be quoted",
			ErrAmbiguousRate, q.StockItemID, q.On, q.Qty.String(), ties,
			found[0].listName, found[1].listName)
	}

	hundred := decimal.NewFromInt(100)
	// Net = rate less the discount, rounded to 4 places — the scale of
	// stock_movement.rate. Rounding here rather than at the voucher line means
	// the number quoted to the user and the number stored on the movement are
	// the same number; rounding twice is how a printed invoice and its posting
	// end up a paisa apart.
	net := best.rate.Sub(best.rate.Mul(best.discount).Div(hundred)).Round(4)

	return PriceQuote{
		PriceListID:    best.listID,
		PriceListName:  best.listName,
		PartyScoped:    best.scoped,
		Rate:           best.rate,
		DiscountPct:    best.discount,
		NetRate:        net,
		QtyFrom:        best.qtyFrom,
		QtyTo:          best.qtyTo,
		QtyToUnbounded: best.unbounded,
	}, nil
}
