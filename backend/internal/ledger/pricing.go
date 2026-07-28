package ledger

// pricing.go — WHAT WE CHARGE, WHEN THE OPERATOR DID NOT SAY.
//
// Migration 0030 and internal/masters/price_list.go gave the system price lists
// — a named set of rates, in force over a half-open date window, optionally
// narrowed to a party group, optionally broken into quantity slabs — and
// NOTHING CALLED THEM. This file is the voucher path's side of that: an item
// invoice line that omits its rate is priced from the effective list, inside
// post()'s transaction, before the taxable value that the GST lines and the
// stock movements both derive from is computed.
//
// ===========================================================================
// OMITTED IS NOT ZERO, AND THE DIFFERENCE IS THE WHOLE FEATURE
// ===========================================================================
// A rate of ZERO IS A STATED FACT. Free samples are real, warranty replacements
// are real, and an operator who keys a rate of 0 has said something specific
// about that line. An OMITTED rate says nothing at all and means "price it for
// me". decimal.Decimal has no "absent", so the absence is carried in
// InventoryLine.RateUnstated — see that field for why its zero value means
// "stated" and why the opposite polarity would silently reprice every free
// sample the importer has ever sent.
//
// ===========================================================================
// NO EFFECTIVE PRICE IS A NAMED ERROR, NEVER A ZERO
// ===========================================================================
// If no list covers (item, party group, date, quantity), an omitted rate CANNOT
// be resolved, and the only two options are to refuse the voucher or to invent
// a number. Inventing zero is the catastrophic one: the invoice posts, it
// balances, the goods leave the warehouse, the GST on a taxable value of zero
// is zero, the return files clean, and the customer is billed nothing. Nothing
// anywhere reports an error, because nothing anywhere is wrong — the arithmetic
// is perfect over a price that was never a price. So this refuses with
// ErrNoEffectivePrice, naming the item, the quantity and the date, which are
// the three things that decide which list applies and therefore the three
// things the operator has to fix.
//
// masters.ErrNoRate makes the same argument at the master layer. The two
// sentinels are separate because a caller of the ledger cannot import a
// masters-layer error to classify a POST failure, and collapsing them would put
// a package dependency in the way of an HTTP status code.
//
// ===========================================================================
// A STATED RATE THAT DISAGREES WITH THE LIST IS THE OPERATOR'S CALL
// ===========================================================================
// It is honoured, it is not an error, and the price list is NOT consulted for
// it. Overriding a rate is an ordinary act of business — a negotiated deal, a
// clearance, a price agreed on the phone — and refusing it would make the
// price list a cage rather than a default.
//
// IS THE VARIANCE RECORDED? NO, AND THAT IS A KNOWN GAP, NOT AN OVERSIGHT.
// "Why did this invoice go out at the wrong price" wants a durable answer, and
// there is exactly one place a durable answer belongs: the movement row, as the
// price list the line was quoted from plus whether the operator overrode it.
// stock_movement has no such columns, and adding them is a migration PLUS a new
// field on CanonicalStockLine — which is a change to the hashed payload, and the
// hash chain is a one-way door (hash.go says so in those words). Neither is in
// this slice's reach.
//
// What this file deliberately does NOT do is resolve the price anyway and throw
// the answer away. That would spend a query per line, be unobservable by any
// test, and leave a reader believing the provenance was captured somewhere.
// A gap that is written down is cheaper than a gap that looks closed.
//
// ===========================================================================
// WHY THIS QUERY IS NOT A CALL TO masters.Service.ResolveRate
// ===========================================================================
// ResolveRate opens its OWN transaction (s.runner.RunInTx). Calling it from
// inside post()'s transaction would take a SECOND pooled connection while the
// first is still held — the classic way to deadlock a request under a pool
// smaller than the concurrency — and the rate it read would be from a snapshot
// causally unrelated to the movements about to be written.
//
// So the lookup is spelt again here against post()'s own tx. That is a
// duplicated query, and duplicated queries drift; TestTheLedgerPriceMatchesThe
// MastersResolver pins the two against each other over the same fixtures, so a
// change to either that is not made to both fails a test rather than quietly
// pricing invoices two ways. The right end state is a tx-scoped resolver in
// masters that both callers share — reported as a follow-up, since
// internal/masters is outside this slice's files.

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// ErrNoEffectivePrice refuses a line that asked to be priced when no price list
// covers it. See the header: the alternative is an invoice for nothing that
// balances perfectly.
var ErrNoEffectivePrice = errors.New(
	"ledger: no price list covers this line, and a rate that was not stated cannot be invented")

// ErrAmbiguousPrice refuses a line that two price lists of the SAME specificity
// both answer.
//
// Migration 0030's two exclusion constraints make this unreachable: at most one
// list is in force per (company, party-group scope) on a date, and within a list
// at most one slab contains a quantity. It is checked anyway for the reason
// masters.ErrAmbiguousRate is: the day somebody drops a constraint to push a
// data migration through, the failure mode WITHOUT this check is silent — an
// arbitrary one of the two rates, chosen by the query planner, on real invoices
// that nobody can reproduce afterwards.
var ErrAmbiguousPrice = errors.New(
	"ledger: more than one price list of equal scope quotes this line, so no rate can be quoted")

// priceQuote is the single deterministic answer to "what do we charge".
//
// Rate and DiscountPct are kept apart rather than pre-netted for the reason
// migration 0030 stores them apart: the invoice has to print both. "1,000.00
// less 5%" is a different document from "950.00", and a customer who negotiated
// a trade discount will ask where it went.
type priceQuote struct {
	listID      uuid.UUID
	listName    string
	partyScoped bool
	rate        decimal.Decimal
	discountPct decimal.Decimal
}

// partyGroupOfLedger returns the account group a party ledger sits under, which
// is the scope a party-narrowed price list is matched on.
//
// The group is matched EXACTLY and the account_group tree is NOT walked, which
// is the rule masters.PriceQuery states and this must not quietly differ from:
// walking it would make "which list applied" depend on the shape of somebody's
// chart of accounts, so re-parenting a debtor group would silently re-price
// every customer under it.
//
// A voucher with no party ledger, or one whose ledger has vanished, resolves to
// uuid.Nil — "no party in hand" — and only the company-wide list is considered.
// That is a narrowing, never a widening: the worst case is that a customer is
// quoted the company rate instead of their group's, which is visible on the
// invoice, rather than a stranger being quoted somebody else's deal.
func partyGroupOfLedger(ctx context.Context, tx pgx.Tx, companyID, partyLedgerID uuid.UUID) (uuid.UUID, error) {
	if partyLedgerID == uuid.Nil {
		return uuid.Nil, nil
	}
	var group uuid.UUID
	switch err := tx.QueryRow(ctx,
		`SELECT group_id FROM ledger WHERE company_id = $1 AND id = $2`,
		companyID, partyLedgerID).Scan(&group); {
	case errors.Is(err, pgx.ErrNoRows):
		return uuid.Nil, nil
	case err != nil:
		return uuid.Nil, fmt.Errorf("ledger: read the party's account group for pricing: %w", err)
	}
	return group, nil
}

// resolvePrice answers "what do we charge for this item, to this party group, on
// this date, at this quantity" with exactly one rate or a typed error.
//
// DETERMINISM COMES FROM THE SCHEMA, NOT FROM AN ORDER BY TIE-BREAK.
// price_list_no_overlap_in_scope means at most ONE list is in force per
// (company, party-group scope) on the date; price_list_entry_slabs_disjoint
// means at most ONE slab of that list contains the quantity. So there are at
// most two candidate rows in all — one company-wide, one party-scoped — and the
// party-scoped one wins, which is the only precedence in this design and cannot
// tie because there are only two levels. Two rows at the SAME level is a genuine
// ambiguity and is refused, not resolved.
//
// The quantity asked for is the LINE's quantity, because that is what decides
// the slab: a list that charges 100.00 below a hundred units and 90.00 at a
// hundred and above must price an order of 250 at 90.00, and asking on any other
// quantity returns a rate the customer was never offered.
func resolvePrice(ctx context.Context, tx pgx.Tx, companyID, itemID, partyGroupID uuid.UUID,
	on string, qty decimal.Decimal) (priceQuote, error) {

	if on == "" {
		// A rate is only meaningful as at a day, because a list has a window.
		// Without a date every list would look equally applicable, including
		// next season's.
		return priceQuote{}, errors.New(
			"ledger: a line priced from the price list needs the voucher date: a list is only in force over a window")
	}

	var partyGroup any
	if partyGroupID != uuid.Nil {
		partyGroup = partyGroupID
	}

	// `pl.party_group_id = $2` is NULL-safe by omission: when $2 is NULL the
	// comparison is UNKNOWN for every row, so only the company-wide arm
	// (IS NULL) can match — exactly right for a caller with no party in hand.
	rows, err := tx.Query(ctx,
		`SELECT pl.id, pl.name, (pl.party_group_id IS NOT NULL), e.rate, e.discount_pct
		   FROM price_list_entry e
		   JOIN price_list pl
		     ON pl.company_id = e.company_id AND pl.id = e.price_list_id
		  WHERE e.company_id      = $1
		    AND e.stock_item_id   = $2
		    AND (pl.party_group_id = $3 OR pl.party_group_id IS NULL)
		    AND pl.effective @> $4::date
		    AND e.qty_range  @> $5::numeric
		  ORDER BY (pl.party_group_id IS NOT NULL) DESC`,
		companyID, itemID, partyGroup, on, qty)
	if err != nil {
		return priceQuote{}, fmt.Errorf("ledger: price lookup: %w", err)
	}
	defer rows.Close()

	var found []priceQuote
	for rows.Next() {
		var q priceQuote
		if err := rows.Scan(&q.listID, &q.listName, &q.partyScoped, &q.rate, &q.discountPct); err != nil {
			return priceQuote{}, fmt.Errorf("ledger: scan price lookup: %w", err)
		}
		found = append(found, q)
	}
	if err := rows.Err(); err != nil {
		return priceQuote{}, fmt.Errorf("ledger: iterate price lookup: %w", err)
	}

	if len(found) == 0 {
		return priceQuote{}, fmt.Errorf("%w: item %s, quantity %s, on %s",
			ErrNoEffectivePrice, itemID, qty.String(), on)
	}

	// Rows arrive party-scoped first, so found[0] is the most specific answer.
	// Anything else at that same specificity is the ambiguity.
	best := found[0]
	ties := 0
	for _, q := range found {
		if q.partyScoped == best.partyScoped {
			ties++
		}
	}
	if ties > 1 {
		return priceQuote{}, fmt.Errorf(
			"%w: item %s on %s at quantity %s is covered by %d lists of equal scope (%s, %s)",
			ErrAmbiguousPrice, itemID, on, qty.String(), ties, found[0].listName, found[1].listName)
	}
	return best, nil
}
