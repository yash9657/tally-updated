package ledger

// fulfilment.go — WHICH ORDER THIS SHIPMENT SATISFIES, as an input to the
// voucher path.
//
// A delivery note says "5 Tables went out". It has never been able to say which
// of the customer's orders those 5 came off, and that link is the only thing an
// outstanding balance can be computed from — the number a salesperson quotes,
// the number a warehouse picks against, and the number a business is judged on
// when it misses a promise. Migration 0042 adds it. This file is the seam
// between a request and BOTH renderings of it: CanonicalPayload.Fulfilments
// (hashed, signed, part of the voucher's audit identity) and order_fulfilment
// (queryable, indexed, what the order book reads).
//
// It is modelled on cost_allocation.go, which is the worked example in this
// package for "a value that rides in the hashed payload AND in columns, from one
// source in one transaction". The reasoning there applies verbatim: both
// renderings are built from ONE in-memory slice inside ONE transaction by
// ledger.post(), so no code path exists that can write one without the other,
// and the signature can never disagree with the rows.
//
// ===========================================================================
// ⛔⛔ AN ORDER IS A COMMITTED QUANTITY, NOT A MOVEMENT
// ===========================================================================
// The stock ledger has exactly one primitive — a positive quantity plus a
// direction — and an order is neither. Nothing has moved when a customer orders
// 20 units. So an order never touches stock_movement, never reaches closing
// stock, and never reaches the balance sheet; see migration 0042's header, which
// states the consequence of getting this wrong (closing stock corrupted by every
// open order, reconciling against nothing).
//
// ⛔ AND THEREFORE: A FULFILMENT QUANTITY IS A LINK, NOT A MOVEMENT.
// FulfilmentInput.Qty says how much of an ORDER this line consumed;
// InventoryLine.Qty says how much STOCK moved. They are usually equal and they
// are NOT the same fact. Closing stock reads the movement, the order book reads
// this, and neither reads the other.
//
// That distinction is what makes refusing over-fulfilment cost nothing, which is
// the decision PLAN-TALLY-PARITY.md §T3 requires to be stated once. An operator
// who genuinely ships 105 against a 100-unit order records a movement of 105 —
// the goods really left, closing stock is right — and a link of 100. The extra 5
// consumed no order, which is exactly true: nobody ordered them. So the refusal
// never prevents recording what happened; it only prevents CLAIMING that more of
// an order was consumed than the order ever committed, which would drive the
// outstanding balance negative and "the customer owes us -5 units of promise" is
// not a fact about anything.
//
// ===========================================================================
// WHY THIS IS HASHED, WHICH IS WHERE IT DIFFERS FROM 0045 AND 0069
// ===========================================================================
// A fulfilment is OPERATOR-KEYED INPUT: someone chose, at entry, which order
// this shipment satisfies. That places it with inward_doc and NOT with
// bank_reconciliation (0045) or tds_challan (0069). Those two key on voucher_id
// and store what they matched precisely BECAUSE they are metadata added later by
// a different act, and can therefore be left stale by an ALTER they never see.
//
// A fulfilment cannot be left stale: an ALTER RE-STATES it from the new input in
// the same transaction, so an alter that moves 5 units to a different order line
// says so in the signed bytes, and the outstanding balance recomputes from the
// current version with nothing to detect and nothing to repair. That is why
// order_fulfilment keys on (voucher_version_id, line_no) — the same key
// stock_movement uses — and why every reader must join
// voucher_head.current_version_id or count a superseded delivery twice.
//
// ===========================================================================
// WHY THESE CHECKS EXIST AT ALL, GIVEN THE DATABASE ALREADY REFUSES
// ===========================================================================
// Migration 0042's order_fulfilment_within_commitment is a CONSTRAINT TRIGGER,
// DEFERRABLE INITIALLY DEFERRED, and it is THE AUTHORITY. It has to be deferred:
// during an ALTER the new version's rows are inserted while the superseded
// version's are still present and current_version_id is still moving, so a
// per-statement check would refuse a correction that merely re-stated the same
// quantity.
//
// The real reason to check early is smaller and still sufficient: a constraint
// name is not a sentence, and a deferred trigger's message arrives attached to a
// rolled-back transaction rather than to the line the operator keyed. So these
// checks run BEFORE the write for the same reason validateCostAllocations does —
// the DB decides, this layer explains.
//
// ⚠️ AND THE DIVISION OF LABOUR IS DELIBERATE. Three of 0042's invariants are
// NOT checked here and cannot be, because each needs a row this layer does not
// hold: that the order line names the SAME stock item as the voucher line, that
// a SALES order is fulfilled by goods going OUT (and a PURCHASE by goods coming
// IN), and that the cumulative fulfilment across OTHER vouchers stays within the
// commitment. All three are enforced by the trigger, which is the only place
// that can see them. This file checks what is decidable from the request alone,
// and says so rather than implying it has checked everything.

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// FulfilmentInput is ONE inventory line of this voucher consuming ONE order
// line.
//
// The three fields are exactly the three the hashed payload carries, in the same
// order, because CanonicalFulfilment is built from this struct verbatim and
// nothing may be present in one and absent from the other.
type FulfilmentInput struct {
	// LineNo is this voucher's 1-BASED inventory line, matching
	// InventoryLine's position and stock_movement.line_no.
	//
	// ⛔ 1-BASED, WHERE CostAllocationInput.PostingSeq IS 0-BASED. The asymmetry
	// is inherited and not a choice available here: stock_movement.line_no has
	// been a 1-based column since migration 0025 and the hashed
	// CanonicalStockLine has carried it as such ever since, while the posting
	// sequence is an index into a JSON array that has never been stored as a
	// column at all. Making this one 0-based to match its neighbour would put
	// two different spellings of the same voucher line in one payload.
	//
	// Unlike PostingSeq this names a line the CLIENT STATED on every class: an
	// item invoice generates its POSTINGS inside the transaction but its
	// INVENTORY comes straight from the operator's grid. That is why an item
	// invoice may carry fulfilments and may not carry cost allocations — see
	// validateFulfilments and the guard in post().
	LineNo int
	// OrderLineID is the order LINE being consumed, never the order. Fulfilment
	// is per item: a delivery note routinely completes one line of a three-line
	// order and leaves the rest outstanding, and an order-level link could not
	// say which.
	//
	// Never uuid.Nil. The all-zeros UUID PARSES (HANDOFF §7.3), so it is refused
	// explicitly rather than left to the foreign key — as a real grouping key it
	// would let two lines whose ids failed to serialise be summed together
	// against one non-existent order.
	OrderLineID uuid.UUID
	// Qty is how much of the ORDER this line consumed — a MAGNITUDE, never a
	// sign: order_head.direction carries the sense, exactly as
	// stock_movement.qty leaves the sign to `direction`. Partial fulfilment is
	// the normal case, so this is very often less than the order line's
	// quantity, and that difference is the whole point of the feature.
	//
	// decimal.Decimal, never float64 — the per-line check below is exact and a
	// float would make "12 + 8 = 20" a coin flip.
	Qty decimal.Decimal
}

// Canonical returns the hashed view of one fulfilment.
//
// Every field becomes a STRING, including the line number, for the reason
// CanonicalStockLine records: VerifyChain re-derives the hashed bytes by
// decoding the stored JSONB back into the struct and re-marshalling it, and that
// re-derivation is exact only while no value has a second legal spelling. The
// quantity is StringFixed(4) so its SCALE survives — 5 and 5.0000 are the same
// number and different bytes, and a jsonb column does not preserve the text it
// was given.
func (f FulfilmentInput) Canonical() CanonicalFulfilment {
	return CanonicalFulfilment{
		LineNo:      fmt.Sprintf("%d", f.LineNo),
		OrderLineID: f.OrderLineID.String(),
		Qty:         f.Qty.StringFixed(4),
	}
}

// canonicalFulfilments builds the hashed slice, or NIL.
//
// ⛔ THE NIL RETURN IS LOAD-BEARING, NOT TIDINESS. `fulfilments,omitempty` omits
// a nil slice and an empty one alike, but only because encoding/json treats
// len == 0 as empty — a caller that unconditionally built
// make([]CanonicalFulfilment, 0) would be relying on that, and a single stray
// `[]` in the payload changes the hash of every voucher it touches while looking
// completely correct at the call site. Every voucher posted before this session
// carries no fulfilments and must marshal to BYTE-IDENTICAL JSON to what it
// always did, or VerifyChain retroactively reports every book in the field as
// tampered with — which is indistinguishable from an attack.
// TestAVoucherWithNeitherAllocationsNorFulfilmentsHashesExactlyAsBefore is the
// test that can see it.
func canonicalFulfilments(fs []FulfilmentInput) []CanonicalFulfilment {
	if len(fs) == 0 {
		return nil
	}
	out := make([]CanonicalFulfilment, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Canonical())
	}
	return out
}

// fulfilmentPairKey identifies ONE link: one voucher line, one order line. The
// duplicate check is per key.
type fulfilmentPairKey struct {
	lineNo      int
	orderLineID uuid.UUID
}

// validateFulfilments refuses a link the database would refuse anyway, but with
// a sentence naming the line and the quantities instead of a constraint name
// after a rollback.
//
// `inventory` is the FINAL inventory list of the version being written. For a
// Physical Stock voucher that list is generated inside the transaction from the
// counted positions (buildPhysicalStockDeltas), which is one of the reasons
// post() calls this again inside the transaction after the generator has run.
//
// A voucher line with NO fulfilment is LEGAL and stays legal. Most stock that
// moves in most businesses was never ordered in advance — an over-the-counter
// sale, a stock transfer between godowns, a manufacture — and 0042's trigger
// says nothing about a line with no fulfilment rows for exactly that reason. So
// this function checks only the LINKS THAT EXIST: it never requires one.
func validateFulfilments(inventory []InventoryLine, fs []FulfilmentInput) error {
	if len(fs) == 0 {
		return nil
	}

	// Pass 1 — each link on its own: does it point at a real inventory line of
	// THIS voucher, does it name an order line, and is it a positive magnitude.
	//
	// This runs first because an out-of-range line makes every later check
	// meaningless (there is no moved quantity to compare against), and because a
	// client that sent a 0-based index is the single most likely defect on this
	// path — LineNo is 1-based here and its neighbour CostAllocationInput.PostingSeq
	// is 0-based, so an off-by-one is a mistake a careful client will still make.
	// It must NOT reach Postgres, where it would arrive as a violation of
	// order_fulfilment_movement_fk — an error naming a constraint the operator
	// has never heard of, for a mistake entirely about the form they just filled
	// in.
	for _, f := range fs {
		if f.LineNo < 1 || f.LineNo > len(inventory) {
			if len(inventory) == 0 {
				return fmt.Errorf(
					"ledger: a fulfilment names inventory line %d but this voucher moves no stock at all, so there is nothing that could have satisfied an order — an order is fulfilled by goods, and a voucher with no item lines ships none",
					f.LineNo)
			}
			return fmt.Errorf(
				"ledger: a fulfilment names inventory line %d but this voucher has %d item line(s), numbered 1 to %d — inventory lines are 1-BASED here (unlike a cost allocation's posting number, which is 0-based), so a client that sent an index rather than a line number will be exactly one out",
				f.LineNo, len(inventory), len(inventory))
		}
		if f.OrderLineID == uuid.Nil {
			return fmt.Errorf(
				"ledger: the fulfilment on inventory line %d names no order line, so nothing is recorded about which commitment the goods satisfied and the order it came from would stay outstanding forever",
				f.LineNo)
		}
		if !f.Qty.IsPositive() {
			return fmt.Errorf(
				"ledger: the fulfilment of %s against order line %s on inventory line %d must be more than zero — a fulfilment is a MAGNITUDE and the order's own direction carries the sense, so a zero or negative link either records that an order was not fulfilled at all or un-delivers goods that went out",
				f.Qty.StringFixed(4), f.OrderLineID, f.LineNo)
		}
	}

	// Pass 2 — the same order line claimed twice by the same voucher line.
	//
	// order_fulfilment_one_per_pair refuses it, but the reason is arithmetic
	// rather than bookkeeping neatness: the same link keyed twice can only make a
	// line's total come out right by making the FIRST entry wrong, so an operator
	// would "fix" a quantity error by corrupting a figure that was correct.
	//
	// ⚠️ IT IS KEYED ON THE PAIR, so one voucher line legitimately fulfilling
	// SEVERAL order lines is untouched — a single 20-unit delivery line covering
	// a 12-unit order and an 8-unit one for the same item is the ordinary case,
	// and CanonicalFulfilment carries one entry each.
	seen := make(map[fulfilmentPairKey]bool, len(fs))
	for _, f := range fs {
		key := fulfilmentPairKey{lineNo: f.LineNo, orderLineID: f.OrderLineID}
		if seen[key] {
			return fmt.Errorf(
				"ledger: inventory line %d claims order line %s twice; the same link keyed twice can only make the line's total come out right by making the other entry wrong",
				f.LineNo, f.OrderLineID)
		}
		seen[key] = true
	}

	// ⛔⛔ PASS 3 — A LINE CANNOT DISCHARGE MORE COMMITMENT THAN IT MOVED. This is
	// the invariant this function exists for and it is the one a naive
	// implementation omits, because every individual order looks fine.
	//
	// A 20-unit delivery line may cover a 12-unit order and an 8-unit one. It may
	// NOT cover a 20-unit order and another 20-unit order: forty units of promise
	// would be discharged by twenty units of goods, neither order would look
	// over-fulfilled, and nothing else in the system could notice. That is the
	// same double-count shape the cost-centre model warns about (a per-posting
	// total across parallel axes), reached from the other direction.
	//
	// A STRICT INEQUALITY, not equality — and this is where it differs from
	// validateCostAllocations, which demands exact equality to the paisa. An
	// incomplete cost split leaves real money unattributed and is always an
	// error; an incomplete link is the ordinary over-delivery case, where some of
	// what shipped was simply not ordered. Requiring equality here would make it
	// impossible to ship 105 against a 100-unit order at all.
	moved := make(map[int]decimal.Decimal, len(fs))
	// order preserves first-appearance order so the same bad request always
	// produces the same sentence; map iteration order would make the message flap
	// between runs on a voucher with two bad lines.
	order := make([]int, 0, len(fs))
	for _, f := range fs {
		if _, ok := moved[f.LineNo]; !ok {
			order = append(order, f.LineNo)
			moved[f.LineNo] = decimal.Zero
		}
		moved[f.LineNo] = moved[f.LineNo].Add(f.Qty)
	}
	for _, lineNo := range order {
		linked := moved[lineNo]
		shipped := inventory[lineNo-1].Qty
		if !linked.GreaterThan(shipped) {
			continue
		}
		return fmt.Errorf(
			"ledger: inventory line %d moves %s but its fulfilments total %s against the orders it names — %s of commitment would be discharged by goods that never moved, and because no single order is over-fulfilled nothing else would report it",
			lineNo, shipped.StringFixed(4), linked.StringFixed(4),
			linked.Sub(shipped).StringFixed(4))
	}
	return nil
}

// writeFulfilments inserts the order_fulfilment rows for one voucher version.
//
// It needs no id mapping, which is where it is SIMPLER than
// writeCostAllocations: order_fulfilment keys on (voucher_version_id, line_no)
// and line_no is the operator's own 1-based line number, carried unchanged from
// the request through the payload into the column. writeCostAllocations has to
// thread a slice of freshly minted posting ids because `posting` has no ordinal
// column at all.
//
// ⛔ IT MUST RUN AFTER THE stock_movement INSERTS of the same version.
// order_fulfilment_movement_fk references stock_movement
// (company_id, voucher_version_id, line_no) and that key only exists once those
// INSERTs have run — and it is the FK that makes line_no a checked fact rather
// than a number the client asserted. It is also what lets 0042's trigger read
// the movement's own direction, item and quantity.
//
// An ALTER writes NEW stock_movement rows under a NEW version (movements are
// immutable), so the version id here is always the version being written and
// never the superseded one's. Attaching a correction's links to the old version
// would leave the order book reading the OLD delivery forever, because it joins
// voucher_head.current_version_id.
func writeFulfilments(ctx context.Context, tx pgx.Tx, companyID, versionID uuid.UUID,
	fs []FulfilmentInput) error {
	for _, f := range fs {
		rowID, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("ledger: new order fulfilment id: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO order_fulfilment
			   (id, company_id, voucher_version_id, line_no, order_line_id, qty)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			rowID, companyID, versionID, f.LineNo, f.OrderLineID, f.Qty,
		); err != nil {
			return fmt.Errorf("ledger: insert order_fulfilment: %w", err)
		}
	}
	return nil
}
