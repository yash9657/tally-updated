package ledger

// nonaccounting.go — THE STOCK VOUCHERS THAT NEVER TOUCH THE LEDGER.
//
// Delivery Note, Receipt Note, Stock Journal and Physical Stock move goods and
// post ZERO accounting lines. In Tally they are first-class voucher types and a
// warehouse operator lives in them all day: goods leave hours or weeks before
// the billing department raises the invoice, and a system that can only record
// a movement by also recording revenue forces the operator to either invent an
// invoice or key nothing at all. Both are worse than the truth.
//
// ===========================================================================
// WHY "IS THIS AN ACCOUNTING CLASS" IS A COMPILE-TIME SET AND NOT A DB FLAG
// ===========================================================================
// voucher_type.is_accounting exists and this file does not read it. It is
// DESCRIPTIVE metadata (what the UI draws, how a report labels a voucher); the
// authority is accountingClasses below, a closed set fixed at compile time.
//
// If double-entry were keyed off the column instead, then one row —
//
//	UPDATE voucher_type SET is_accounting = false WHERE name = 'Sales';
//
// — in a table the admin surface already exposes, in a tenant nobody is
// watching, would make the server accept a SALES INVOICE THAT SHIPS STOCK OUT
// OF THE WAREHOUSE AND POSTS NOTHING AT ALL: no revenue, no receivable, no tax.
//
// And the database could not save us. posting_balanced (migration 0002) is a
// deferred constraint trigger on the posting TABLE, so a voucher that inserts
// ZERO posting rows never fires it. The trial balance would still balance — it
// would simply be missing the sale — and there is no report anywhere that shows
// you a voucher that did not happen. An invariant that must never be
// data-driven does not get a data-driven switch.
//
// TestDoubleEntryIsNeverRelaxedForAnAccountingClass is the executable form of
// this paragraph: it flips the flag in the database and proves the post is
// still refused.
//
// ===========================================================================
// WHY A NON-ACCOUNTING VOUCHER MUST CARRY NO POSTINGS AT ALL
// ===========================================================================
// Skipping validatePostings is only safe when paired with a hard refusal of
// postings on these classes. Skip alone, and a Delivery Note becomes the one
// document in the system that may state UNBALANCED ledger lines — the exact
// hole the skip was supposed to be the absence of. validateStockVoucher
// therefore refuses postings, bill allocations, GST and TDS outright: these
// vouchers have no money side, so anything that looks like one is a mistake
// about what the operator is keying, not a shortcut.
//
// ===========================================================================
// PHYSICAL STOCK POSTS A DELTA, NOT AN ABSOLUTE
// ===========================================================================
// A Physical Stock voucher states what was COUNTED: "there are 13 on the shelf".
// That is an absolute figure, and there are two ways to record it.
//
// An ABSOLUTE row would mean stock_movement gained a third kind of row that
// says "stop summing, the answer is 13". Every reader of the movement ledger
// would then have to know about it — check_stock_not_negative (0025),
// checkNegativeStock (stock.go), inventory.Replay, and every stock report —
// and each of them would have to apply it IN ORDER, because "13, then +5" and
// "+5, then 13" give different answers. That would destroy the one property
// 0025's header calls out explicitly: quantity is ORDER-INDEPENDENT, a plain
// sum, which is precisely why the negative-stock trigger can compute a closing
// balance cheaply and still be correct. Value is order-dependent and is
// therefore never stored; quantity is not, and must stay that way.
//
// So we post the DELTA: counted minus the book position, as one ordinary
// movement, computed inside post()'s transaction from the same closing-position
// query the negative-stock pre-check uses. The ledger stays a pure sum, the
// hash payload records exactly what moved, an ALTER restates it and a DELETE
// erases it — all for free, because a physical stock voucher is, after the
// delta, an entirely ordinary stock voucher.
//
// The counted figure is not lost by recording the delta: the closing position
// immediately after this voucher IS the count, by construction.
//
// A COUNT LOWER THAN THE BOOK IS NOT NEGATIVE STOCK. A shortage posts an
// OUTWARD delta, and the position it leaves behind is exactly the counted
// quantity — which is >= 0 because a count cannot be negative. So a shortage
// commits even when company.allow_negative_stock is false, and it must: the
// count is a FACT about the shelf, not a withdrawal request, and refusing it
// would leave the books permanently asserting stock that a human has just
// physically confirmed is not there. TestPhysicalStockShortageIsNotNegativeStock
// pins this.
//
// ===========================================================================
// ORDERS ARE NOT IN THIS SLICE, ON PURPOSE
// ===========================================================================
// See ErrOrderVouchersNotModelled.

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// The non-accounting stock voucher classes. Each maps to a voucher_type seeded
// by migration 0029 whose parent_class is upper(replace(name,' ','_')) — the
// form ledger.defaultVoucherTypeID resolves.
const (
	// DeliveryNote (Alt+F8): goods LEAVE against a customer, before or without
	// the invoice. Every line is Outward.
	DeliveryNote Class = "Delivery Note"
	// ReceiptNote (Alt+F9): goods ARRIVE from a supplier, before the bill. Every
	// line is Inward.
	ReceiptNote Class = "Receipt Note"
	// StockJournal (Alt+F7): a transfer between godowns, or a manufacture that
	// consumes components and produces a finished good. ⭐ IT IS THE ONE CLASS
	// WHOSE VALUE MUST CONSERVE: both sides of the document are the company's own
	// stock and it posts no accounting lines, so what its inward legs are carried
	// at is DERIVED from what its outward legs consumed, in the replay, never
	// from the keyed rate (inventory/conserving.go, defect D103). It is the ONE
	// class that carries BOTH directions on the same voucher — which is exactly why
	// migration 0025 made stock_movement_not_negative DEFERRABLE INITIALLY
	// DEFERRED: checked per statement, the OUT line would raise before the IN
	// line had been written and a balanced transfer would be refused or accepted
	// depending on the order its lines happened to be inserted in.
	StockJournal Class = "Stock Journal"
	// PhysicalStock (Alt+F10): the counted figure. It carries PhysicalCount
	// lines, not Inventory lines; the server turns the count into a delta
	// movement inside the post transaction. See the header.
	PhysicalStock Class = "Physical Stock"
)

// SalesOrder and PurchaseOrder are NAMED BUT NOT POSTABLE. They exist as
// constants so that the refusal in post() can be specific about what is
// missing, and so that a reader looking for "where are the orders?" finds the
// answer here rather than concluding they were forgotten. Neither is in
// validClasses; migration 0029 deliberately seeds no voucher_type for them.
const (
	// SalesOrder (Ctrl+F8): a customer's commitment to buy. NOT POSTABLE — see
	// ErrOrderVouchersNotModelled.
	SalesOrder Class = "Sales Order"
	// PurchaseOrder (Ctrl+F9): our commitment to buy. NOT POSTABLE — see
	// ErrOrderVouchersNotModelled.
	PurchaseOrder Class = "Purchase Order"
)

// ErrOrderVouchersNotModelled refuses a Sales/Purchase Order, because an ORDER
// IS NOT A MOVEMENT and this schema can only record movements.
//
// stock_movement has exactly one primitive — a positive quantity plus a
// direction — and every reader of it SUMS. There is no way to say "10 units are
// promised to a customer but are still physically on the shelf" in a sum. An
// order recorded as a movement would let a QUOTATION deplete the warehouse and
// then refuse the real sale for negative stock; an order recorded as a zero
// movement would record nothing at all.
//
// What an order needs is a COMMITTED QUANTITY: its own table, its own
// fulfilment linkage (an order is closed by the Delivery Notes that ship it,
// partially and over time), and a second pair of columns beside the physical
// balance in every stock report — which is the shape TallyPrime uses, where
// "Orders Pending" sits BESIDE the balance rather than inside it. Modelling
// that badly now would be far more expensive than not modelling it yet, so the
// class is refused with a message that says what is missing.
var ErrOrderVouchersNotModelled = errors.New(
	"ledger: sales/purchase orders are a COMMITTED quantity, not a stock movement, and the movement ledger cannot express one; " +
		"an order voucher would deplete the warehouse for a quotation, so it is refused until committed quantity is modelled")

// accountingClasses is THE CLOSED, COMPILE-TIME SET of classes that must state
// balanced double-entry postings. Nothing reads voucher_type.is_accounting to
// decide this, and nothing ever may — see the header for what one UPDATE would
// otherwise buy an attacker (or a careless admin).
//
// Adding a class here is a code change, in a diff, with a reviewer. That is the
// entire point.
// Payroll is here, and its membership is the load-bearing half of the payroll
// slice's ledger decision. A pay run is the largest single debit most small
// companies raise in a month; classified as non-accounting it would take the
// !postsDoubleEntry branch, validateStockVoucher would REFUSE its postings
// outright, and a caller that dropped them to satisfy the refusal would commit
// a voucher with ZERO posting rows — which posting_balanced, being a constraint
// trigger on the posting TABLE, would never fire on. The trial balance would
// still balance; it would simply be missing the payroll. See payroll_class.go.
var accountingClasses = map[Class]bool{
	Contra: true, Payment: true, Receipt: true,
	Journal: true, Sales: true, Purchase: true,
	CreditNote: true, DebitNote: true,
	Payroll: true,
}

// postsDoubleEntry reports whether a class must state balanced double-entry
// postings. It is the ONLY place the accounting/non-accounting question is
// answered, so there is exactly one line to read (and exactly one line to get
// wrong) rather than a predicate re-derived at each call site.
func postsDoubleEntry(c Class) bool { return accountingClasses[c] }

// stockOnlyClasses is the complement within validClasses: the classes that move
// goods and post nothing. Kept as its own set rather than derived as
// "!accounting" so that a class added to validClasses without being classified
// either way is a compile-time-visible omission rather than a silent grant of
// the no-postings-required behaviour.
var stockOnlyClasses = map[Class]bool{
	DeliveryNote: true, ReceiptNote: true,
	StockJournal: true, PhysicalStock: true,
}

// fixedStockDirection returns the ONE direction a class's lines must all take,
// and whether the class has one at all.
//
// A Delivery Note carrying an inward line is a Receipt Note that was keyed into
// the wrong screen: it would ADD stock to the warehouse under a document that
// says "delivered", and no later report could untangle it. Stock Journal has no
// fixed direction on purpose — a transfer is an OUT and an IN, and a
// manufacture consumes three components to produce one finished good.
//
// Physical Stock has none either, but for a different reason: its movement is
// GENERATED (the delta), and whether that comes out inward or outward is a fact
// about the shelf, not a choice the operator makes.
func fixedStockDirection(c Class) (Direction, bool) {
	switch c {
	case DeliveryNote:
		return Outward, true
	case ReceiptNote:
		return Inward, true
	}
	return "", false
}

// PhysicalCountLine is ONE counted position of a Physical Stock voucher: "there
// are Qty of this item, in this godown, from this batch, on the shelf".
//
// It is a COUNT, not a movement, which is why it has no Direction: a count
// carries no sign, and the sign of the correction is derived by comparing it to
// the books. Qty may legitimately be ZERO — "the shelf is empty" is a count,
// and it is the one an auditor most wants recorded.
type PhysicalCountLine struct {
	StockItemID uuid.UUID
	// GodownID / BatchID are the same optional dimensions an InventoryLine
	// carries; uuid.Nil means "not tracked" and matches the SQL NULL position.
	GodownID uuid.UUID
	BatchID  uuid.UUID
	// Qty is the COUNTED quantity, >= 0.
	Qty decimal.Decimal
	// Rate is the cost a SURPLUS is brought in at. It is required (non-zero)
	// only when the count exceeds the books, and ignored otherwise — see
	// buildPhysicalStockDeltas for why a zero-rate surplus is refused rather
	// than accepted.
	Rate decimal.Decimal
}

// validateStockVoucher is validatePostings' counterpart for a class that posts
// none: it checks that the voucher really has no money side, and that its goods
// move the way the document says they do. It runs BEFORE the transaction opens,
// like every other structural check, so a request that cannot possibly be right
// fails with a domain message instead of a constraint violation.
//
// It is reached only from post()'s !postsDoubleEntry branch, so the class is
// necessarily one of stockOnlyClasses.
func validateStockVoucher(in VoucherInput) error {
	// THE PAIRING THAT MAKES THE SKIP SAFE. Without this, skipping the
	// double-entry check would make these the only vouchers in the system
	// permitted to state UNBALANCED ledger lines.
	if len(in.Postings) > 0 {
		return fmt.Errorf(
			"ledger: a %s posts no accounting lines at all (it records the movement of goods, not money); remove the %d posting(s)",
			in.Class, len(in.Postings))
	}
	if len(in.BillAllocations) > 0 {
		return fmt.Errorf(
			"ledger: a %s creates no outstanding bill: there is no party leg for an allocation to reference (the invoice that follows carries the bill)",
			in.Class)
	}
	if in.GST != nil {
		return fmt.Errorf(
			"ledger: a %s carries no tax: GST is charged by the INVOICE, and deriving it here would file a supply twice",
			in.Class)
	}
	if in.TDS != nil {
		return fmt.Errorf("ledger: a %s deducts no tax at source: there is no payment to deduct from", in.Class)
	}

	if in.Class == PhysicalStock {
		if len(in.Inventory) > 0 {
			return errors.New(
				"ledger: a Physical Stock voucher states what was COUNTED, not what moved; send physical_count lines and let the server derive the correction")
		}
		return validatePhysicalCount(in.PhysicalCount)
	}

	if len(in.PhysicalCount) > 0 {
		return fmt.Errorf("ledger: only a Physical Stock voucher carries counted quantities; a %s states the movement itself", in.Class)
	}
	if len(in.Inventory) == 0 {
		return fmt.Errorf("ledger: a %s exists to record goods moving; it has no inventory lines", in.Class)
	}
	// "PRICE IT FOR ME" HAS NO ANSWER ON A DOCUMENT WITH NO MONEY SIDE. A price
	// list quotes a SELLING or BUYING price, and these vouchers neither sell nor
	// buy — a Delivery Note ships goods the invoice will price later, and a
	// Stock Journal moves them between our own shelves at our own cost. Resolving
	// a sales rate onto a transfer would carry stock at its selling price and
	// book the margin as inventory. Refused here, naming the class, so the
	// operator is told which screen they are in; writeStockMovements refuses it
	// again as the last line of defence for classes this check cannot see.
	for i, l := range in.Inventory {
		if l.RateUnstated {
			return fmt.Errorf(
				"ledger: inventory line %d: a %s is not priced from a price list (it records goods moving, not a sale); state the rate the goods are carried at",
				i+1, in.Class)
		}
		// A STATED ZERO IS NOT A CHEAPER WAY OF SAYING NOTHING — ⛔ ON A TRANSFER,
		// AND ONLY ON A TRANSFER.
		//
		// RateUnstated above catches the line that said nothing at all. This catches
		// the one that said "0.0000", which arrives as a perfectly well-formed rate
		// — the DB CHECK is rate >= 0 — and is exactly what a client sends when an
		// operator tabs past the Rate column: parsing an empty box yields zero, and
		// zero is then STATED on the wire, so the backstop above cannot see it.
		//
		// ⛔ WHY THE CLASS MATTERS, AND WHY THIS IS NOT "REFUSE EVERY ZERO RATE".
		// A ReceiptNote brings goods in from OUTSIDE, and a free sample really does
		// arrive at 0.0000 — a stated fact, not a missing price. Carrying it at
		// nothing is CORRECT: quantity rises, value does not, and
		// TestAnInventoryLineWhoseAmountContradictsItsOwnRateIsRefused pins exactly
		// that as a control. Refusing it would make free samples unkeyable.
		//
		// ===================================================================
		// ⛔⛔ WHAT THIS RATE IS FOR CHANGED UNDER THIS COMMENT — READ THIS.
		// ===================================================================
		// The paragraph that used to live here said the OUT leg removes goods "at
		// the position's running WEIGHTED-AVERAGE cost", so an IN leg at zero
		// destroyed the difference. That was true, and COMPLETE, for the world it
		// was written in on 2026-08-07 — and MIGRATION 0078 FALSIFIED IT by adding
		// FIFO and LIFO. Under a lot queue the outward consumes the oldest (or
		// newest) LAYER, not any average, and no single rate an operator can key
		// conserves value in general. Keying the only rate the product displays —
		// the position's blended closing value / closing qty — CREATED 33.30 on a
		// 10-unit transfer, measured in a browser 2026-08-27 (defect D103).
		//
		// ⭐ NOTE THE PATTERN, because it is the point of this comment: nothing
		// EDITED this rule. A migration two features away made its PREMISE false
		// and everything downstream kept reasoning in the old vocabulary. When you
		// add the fourth costing method, come back here.
		//
		// ⭐ SO THE VALUE IS NO LONGER TAKEN FROM THIS RATE AT ALL. The replay
		// DERIVES what the outward leg actually consumed and carries the inward leg
		// at exactly that, so a Stock Journal conserves BY CONSTRUCTION and no
		// keystroke can make it otherwise — inventory/conserving.go is the rule and
		// the reason. It has to live there and not here: value is never stored, so
		// what a transfer consumed is not KNOWN at post time (a back-dated purchase
		// changes which layers it eats).
		//
		// ⚠️ THE REFUSAL STAYS, AND ITS JOB IS NOW DIFFERENT. On a voucher with
		// several inward legs — a manufacture producing a product and a by-product
		// — the keyed rates are the RATIO the derived consumption cost is split by.
		// A zero says "this leg is worth nothing", which silently hands the whole
		// cost to the other legs and is indistinguishable from an operator tabbing
		// past the box. On a plain transfer there is one inward leg per item, the
		// ratio is 1, and the number has no effect on any figure at all — but a
		// screen that accepted a blank there would be teaching the habit.
		//
		// This is the same rule buildPhysicalStockDeltas applies to a counted
		// surplus (a server-INVENTED inward has no supplier price to appeal to
		// either), and the two now read the same way.
		//
		// OUTWARD IS EXEMPT on every class: an outward is consumed at whatever its
		// position's own valuer says it cost — an average, an oldest layer, a
		// newest layer — and its own stated rate is IGNORED throughout
		// (inventory.Movement.Rate), so a zero there changes no figure anybody
		// reads.
		if in.Class == StockJournal && l.Direction == Inward && l.Rate.IsZero() {
			return fmt.Errorf(
				"ledger: inventory line %d: a %s brings goods IN at a rate of zero; the rate is what splits the consumed cost between the goods this voucher produces, and a zero hands this line's share to the others; state the rate the goods are carried at",
				i+1, in.Class)
		}
	}
	if want, fixed := fixedStockDirection(in.Class); fixed {
		for i, l := range in.Inventory {
			if l.Direction != want {
				return fmt.Errorf(
					"ledger: inventory line %d: a %s moves stock %s, not %s (goods keyed the wrong way would be added to the warehouse by a document that says they left)",
					i+1, in.Class, want, l.Direction)
			}
		}
	}
	return nil
}

// validatePhysicalCount checks the counted lines with no database access.
func validatePhysicalCount(cs []PhysicalCountLine) error {
	if len(cs) == 0 {
		return errors.New("ledger: a Physical Stock voucher records at least one counted position")
	}
	for i, c := range cs {
		if c.StockItemID == uuid.Nil {
			return fmt.Errorf("ledger: physical count line %d: stock item id is required", i+1)
		}
		// A count of zero is legitimate and meaningful; a count below zero is not
		// a quantity anyone can have observed.
		if c.Qty.IsNegative() {
			return fmt.Errorf(
				"ledger: physical count line %d: a counted quantity cannot be negative, got %s (a count of 0 says the shelf is empty)",
				i+1, c.Qty.StringFixed(4))
		}
		if c.Rate.IsNegative() {
			return fmt.Errorf("ledger: physical count line %d: rate must be non-negative, got %s", i+1, c.Rate.StringFixed(4))
		}
	}
	return nil
}

// buildPhysicalStockDeltas turns counted positions into the movements that make
// the books agree with the shelf. It runs INSIDE post()'s transaction, because
// the book position it compares against must be the one the movements are about
// to be written into.
//
// A position whose count already agrees with the books produces NO movement at
// all: stock_movement's CHECK (qty > 0) forbids a zero row, and a zero-quantity
// correction is not a thing that happened. A voucher where every line agrees is
// therefore a voucher with no movements — which is the correct record of a
// stock-take that found nothing wrong.
func buildPhysicalStockDeltas(ctx context.Context, tx pgx.Tx, companyID, voucherID uuid.UUID, counts []PhysicalCountLine) ([]InventoryLine, error) {
	seen := map[stockPosition]bool{}
	out := make([]InventoryLine, 0, len(counts))

	for i, c := range counts {
		p := stockPosition{item: c.StockItemID, godown: c.GodownID, batch: c.BatchID}
		// Two counts of the same shelf would each be differenced against the same
		// book figure and BOTH deltas would be written, doubling the correction.
		// There is also no sane reading of two different answers to "how many are
		// there"; refusing is the only honest response.
		if seen[p] {
			return nil, fmt.Errorf(
				"ledger: physical count line %d: this (item, godown, batch) position is counted twice on the same voucher; a shelf has one count",
				i+1)
		}
		seen[p] = true

		// THE BOOK FIGURE THE CORRECTION IS MEASURED AGAINST, read through the
		// one hoisted spelling of the closing-position query (stock.go) rather
		// than through a second copy of it. That single-sourcing is load-bearing
		// and not tidiness: this call decides what the correction IS, and
		// checkNegativeStock decides whether the result is ALLOWED. Two copies
		// that drifted would difference the count against one book and judge it
		// against another, and the count the operator actually made would be
		// refused for negative stock it did not create.
		//
		// voucherID is EXCLUDED because this runs before voucher_head's pointer
		// moves; see closingPosition's comment for what counting the superseded
		// version would do to a re-counted shelf.
		book, err := closingPosition(ctx, tx, companyID, p, voucherID)
		if err != nil {
			return nil, err
		}
		delta := c.Qty.Sub(book)
		if delta.IsZero() {
			continue
		}

		l := InventoryLine{
			StockItemID: c.StockItemID,
			GodownID:    c.GodownID,
			BatchID:     c.BatchID,
			Rate:        c.Rate,
		}
		if delta.IsPositive() {
			// A SURPLUS AT ZERO COST IS REFUSED, NOT ACCEPTED.
			//
			// This inward is INVENTED by the server — nobody keyed a supplier's
			// price for it — so if we let the rate default to zero we would bring
			// units in at no cost, understating closing stock and overstating the
			// cost of every subsequent issue. That is a misstated balance sheet
			// produced by a stock-take, which is the opposite of what a stock-take
			// is for. An operator who genuinely found extra units knows (or can be
			// told) what they cost; Tally's Physical Stock screen has a Rate column
			// for exactly this reason.
			//
			// ⚠️ THE MESSAGE MUST NOT NAME ONE COSTING METHOD — THAT WAS D104. It
			// used to say the surplus "would write the item's WEIGHTED-AVERAGE value
			// down", and it said it on FIFO and LIFO items too, where there is no
			// weighted average to write down: a zero-cost surplus lands as a lot
			// layer worth nothing and drags the position's value down all the same.
			// The RULE was right on all three methods; only its REASON named one.
			// The wording below states the CONSEQUENCE — the item is carried at
			// less than it is worth — which is true on every method there is, and
			// stays true when the fourth arrives. Same pattern as D103 above:
			// migration 0078 falsified a premise and the prose did not follow.
			//
			// The mirror case needs no rate: an outward is consumed at whatever the
			// item's own valuer says it cost and its own stated rate is IGNORED
			// (see inventory.Movement.Rate).
			if c.Rate.IsZero() {
				name := itemNameOrID(ctx, tx, companyID, c.StockItemID)
				return nil, fmt.Errorf(
					"ledger: physical count line %d (%s): the count is %s above the books, and bringing a surplus in at zero cost would carry the extra units at nothing and write the item's stock value down; state the rate the extra units are carried at",
					i+1, name, delta.StringFixed(4))
			}
			l.Direction = Inward
			l.Qty = delta
		} else {
			l.Direction = Outward
			l.Qty = delta.Neg()
		}
		// Informational only on this class — nothing ties it to a taxable base
		// (migration 0027 short-circuits with no GST lines) and the valuer derives
		// value from qty/rate itself. Written anyway so the movement row and the
		// hashed payload state what the correction was worth at the stated rate.
		l.Amount = l.Qty.Mul(l.Rate).Round(moneyScale)
		out = append(out, l)
	}
	return out, nil
}

// itemNameOrID resolves a stock item's name for an error message, falling back
// to the id. Best effort: the refusal is authoritative whether or not the master
// can be read, so a lookup failure must never mask it.
func itemNameOrID(ctx context.Context, tx pgx.Tx, companyID, itemID uuid.UUID) string {
	var name string
	_ = tx.QueryRow(ctx,
		`SELECT name FROM stock_item WHERE company_id = $1 AND id = $2`,
		companyID, itemID).Scan(&name)
	if name == "" {
		return itemID.String()
	}
	return name
}
