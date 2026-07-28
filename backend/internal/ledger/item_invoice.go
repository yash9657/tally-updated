package ledger

// item_invoice.go — THE ITEM INVOICE: the stock lines DERIVE the accounting.
//
// This is where "the books and the stock can never disagree" stops being a
// convention and becomes structural.
//
// THE HOLE THIS CLOSES
// --------------------
// deriveAndVerifyGST verifies the three TAX-ledger amounts against the pure
// compliance engine and NOTHING verified the taxable base they were derived
// from. A client could post 100 units of stock against a Rs 1 invoice: the
// movement ledger would record the goods leaving, the GST params would declare
// a taxable value of 1, the derived tax would agree with the posted tax (both
// computed from the same wrong 1), the double-entry check would pass, and every
// layer would report success. Nothing anywhere would notice that the warehouse
// and the ledger were now telling different stories.
//
// THE FIX: ONE COMPUTATION, ONE TRANSACTION
// -----------------------------------------
// When a voucher carries inventory lines AND GST parameters, it is an ITEM
// INVOICE and the SERVER generates both the GST lines and the accounting
// postings FROM the stock lines:
//
//	per line   taxable = qty * rate less the line discount  (InventoryLine.TaxableValue)
//	           (hsn, gst_rate_pct) come from the stock_item MASTER, never the request
//	tax        = compliance.TaxDerive over those lines
//	party leg  = SUM(line taxables) + total tax
//	goods leg  = SUM(line taxables)   on the sales/purchase ledger
//	tax legs   = the derived CGST/SGST/IGST on the ledgers the caller named
//
// The SAME per-line taxable becomes the movement's `amount` and the GST line's
// `taxable_value`, so migration 0027's deferred tie holds by construction
// rather than by luck. There is exactly one number, computed once, written to
// three places inside one transaction.
//
// WHY GENERATION, NOT VERIFICATION
// --------------------------------
// Verifying a client's postings against the stock lines would be weaker for a
// subtle reason: a verifier can only reject what it thought to look at, and the
// bug above is precisely a thing nobody thought to look at. Generation has no
// gap to leave — the client cannot state a taxable base at all, so it cannot
// state a wrong one. Sending BOTH inventory lines and gst.lines (or postings)
// is therefore an ERROR, not a merge: silently preferring one input over the
// other is how the Rs 1 invoice gets back in.
//
// WHY THE ENGINE IS STILL RE-RUN AND RE-VERIFIED AFTERWARDS
// --------------------------------------------------------
// buildItemInvoice derives the tax to build the tax postings, and post() then
// calls deriveAndVerifyGST, which derives it AGAIN and checks the posted
// amounts. That is not redundant work for its own sake: TaxDerive is pure and
// cheap, and the second pass is what catches a builder that put CGST on the
// IGST ledger, or that let the goods ledger and a tax ledger be the same
// ledger. The generator is not trusted just because we wrote it.
//
// WHY ORIENTATION IS A TABLE AND WHY THE TEST CHECKS NAMES
// -------------------------------------------------------
// This project has ALREADY SHIPPED a fully reversed purchase once (WS8b). It
// survived every check because the double-entry rule only cares that the two
// sides are equal and deriveAndVerifyGST sums posting amounts WITHOUT looking
// at dr_cr — so a purchase posted sales-shaped balances, verifies, and files.
// The only thing that catches it is resolving the posted ledger ids back to
// NAMES and asserting that the PURCHASES ledger is on DEBIT and the supplier on
// CREDIT. invoiceOrientation exists so that "which way round is a purchase" is
// one small table a reader can check by eye instead of a shape implied by the
// order of four literals.

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/compliance"
)

// ErrInventoryAndGSTLines refuses a voucher that carries BOTH stock lines and
// client-supplied gst.lines. On an item invoice the taxable lines ARE the stock
// lines; accepting both would mean choosing which one to believe, and whichever
// we chose the other would be silently discarded — the Rs 1 invoice with 100
// units of stock is exactly that discard. The HTTP layer maps every non-
// ErrNoCompany post error to 422.
var ErrInventoryAndGSTLines = errors.New(
	"ledger: an item invoice derives its GST lines from the stock lines; send inventory OR gst.lines, not both")

// ErrInventoryAndPostings refuses a voucher that carries BOTH stock lines (with
// GST) and client-supplied postings, for the same reason: the postings of an
// item invoice are GENERATED from the goods. A client that also sends its own
// is asserting a second, independent set of amounts, and the whole point of
// this slice is that a second set cannot exist.
//
// NOTE for the alter path: GetVoucher projects the generated postings, so a
// client that re-posts a fetched item invoice VERBATIM will hit this. An item
// invoice is restated by resending its inventory + gst + item ledger, never its
// postings — see the wire-shape note handed to the wiring slice.
var ErrInventoryAndPostings = errors.New(
	"ledger: an item invoice derives its postings from the stock lines; do not send postings with inventory + gst")

// invoiceOrientation is WHICH SIDE each leg of an item invoice lands on. Three
// fields, one row per class, so a reversed purchase is a visible diff instead of
// an emergent property of where four literals happened to be typed.
//
// A Sales invoice: Dr the customer (they owe us), Cr revenue, Cr the output-tax
// heads (we owe the government). A Purchase is the MIRROR, not a copy: Cr the
// supplier (we owe them), Dr the expense, Dr the input-tax heads (the
// government owes us). The notes reverse their parent document, which is why
// CreditNote reads as a purchase and DebitNote as a sale here.
type invoiceOrientation struct {
	party DrCr
	goods DrCr
	tax   DrCr
}

// orientationFor returns the posting sides for a class of item invoice.
//
// Deliberately does NOT also carry the stock direction (see
// expectedStockDirection): keeping them apart is what makes the "reuse the
// sales-shaped builder for purchase" mutation reach the accounting orientation
// ALONE, so the test proves the name-resolving assertion is what catches it
// rather than a direction check catching it first for an unrelated reason.
func orientationFor(c Class) (invoiceOrientation, error) {
	switch c {
	case Sales:
		return invoiceOrientation{party: Debit, goods: Credit, tax: Credit}, nil
	case Purchase:
		return invoiceOrientation{party: Credit, goods: Debit, tax: Debit}, nil
	case CreditNote:
		// A sales return: the customer is credited, revenue and output tax are
		// given back.
		return invoiceOrientation{party: Credit, goods: Debit, tax: Debit}, nil
	case DebitNote:
		// A purchase return: the supplier is debited, the expense and the input
		// tax are reversed.
		return invoiceOrientation{party: Debit, goods: Credit, tax: Credit}, nil
	}
	return invoiceOrientation{}, fmt.Errorf("ledger: %s is not an item-invoice class", c)
}

// expectedStockDirection is which way the goods physically move for a class of
// item invoice. A Sales invoice that carried an INWARD line would credit revenue
// while ADDING stock to the warehouse — the sale would inflate both the P&L and
// the closing inventory, which no later report could untangle.
func expectedStockDirection(c Class) Direction {
	switch c {
	case Sales, DebitNote: // goods leave: a sale, or a return to the supplier
		return Outward
	default: // Purchase, CreditNote: goods arrive, or come back from a customer
		return Inward
	}
}

// itemMaster is the MASTER truth about a stock item that an item invoice is not
// permitted to take from the request.
//
// HSN AND RATE COME FROM HERE, ALWAYS. They are the tax IDENTITY of the goods,
// and identity is a property of the item, not of the document that happens to
// mention it. Taking them from the request would let two invoices for the same
// item file under two different HSN codes at two different rates in the same
// GSTR-1 — with the books, the stock and the arithmetic all internally
// consistent, so nothing but a human reading table 12 would ever see it.
type itemMaster struct {
	name    string
	hsn     string
	gstRate decimal.Decimal
}

// loadItemMasters reads the tax identity of every distinct item on the voucher
// in ONE query. An id with no master row is a hard error naming the id: the
// composite FK on stock_movement would refuse it a moment later anyway, but with
// a message about a foreign key rather than about an unknown item.
func loadItemMasters(ctx context.Context, tx pgx.Tx, companyID uuid.UUID, lines []InventoryLine) (map[uuid.UUID]itemMaster, error) {
	ids := make([]uuid.UUID, 0, len(lines))
	seen := map[uuid.UUID]bool{}
	for _, l := range lines {
		if !seen[l.StockItemID] {
			seen[l.StockItemID] = true
			ids = append(ids, l.StockItemID)
		}
	}

	rows, err := tx.Query(ctx,
		`SELECT id, name, COALESCE(hsn, ''), gst_rate_pct
		   FROM stock_item
		  WHERE company_id = $1 AND id = ANY($2)`,
		companyID, ids)
	if err != nil {
		return nil, fmt.Errorf("ledger: read stock item masters: %w", err)
	}
	defer rows.Close()

	out := make(map[uuid.UUID]itemMaster, len(ids))
	for rows.Next() {
		var (
			id uuid.UUID
			m  itemMaster
		)
		if err := rows.Scan(&id, &m.name, &m.hsn, &m.gstRate); err != nil {
			return nil, fmt.Errorf("ledger: scan stock item master: %w", err)
		}
		out[id] = m
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ledger: iterate stock item masters: %w", err)
	}
	for _, id := range ids {
		if _, ok := out[id]; !ok {
			return nil, fmt.Errorf("ledger: item invoice references unknown stock item %s", id)
		}
	}
	return out, nil
}

// isItemInvoice reports whether this post must have its accounting and tax
// GENERATED from its stock lines: a GST-bearing class carrying both inventory
// lines and GST parameters.
//
// A DELETE is excluded for the same reason it writes no postings and no
// movements — its body is empty by definition, so there is nothing to derive.
func isItemInvoice(op Op, in VoucherInput) bool {
	return op != OpDelete && len(in.Inventory) > 0 && in.GST != nil && gstBearing(in.Class)
}

// validateItemInvoice runs the checks that need no database, BEFORE the
// transaction opens, so a structurally impossible request fails fast with a
// domain message.
//
// It replaces validatePostings for an item invoice: the postings do not exist
// yet. They are validated (balanced, non-negative, two-sided) inside the
// transaction once they have been generated, which is a real check of the
// GENERATOR rather than of the client.
func validateItemInvoice(in VoucherInput) error {
	if len(in.GST.Lines) > 0 {
		return ErrInventoryAndGSTLines
	}
	if len(in.Postings) > 0 {
		return ErrInventoryAndPostings
	}
	if in.PartyLedgerID == uuid.Nil {
		return errors.New("ledger: an item invoice needs a party ledger to bill")
	}
	if in.ItemLedgerID == uuid.Nil {
		return errors.New("ledger: an item invoice needs an item ledger (sales/purchase account) to book the goods to")
	}
	if _, err := orientationFor(in.Class); err != nil {
		return err
	}
	want := expectedStockDirection(in.Class)
	for i, l := range in.Inventory {
		if l.Direction != want {
			return fmt.Errorf(
				"ledger: inventory line %d: a %s item invoice moves stock %s, not %s (goods must move the way the money does)",
				i+1, in.Class, want, l.Direction)
		}
	}
	if in.GST.ReverseCharge {
		// REFUSED, NOT SILENTLY MIS-POSTED. Under RCM the supplier charges no
		// tax, so the party leg excludes it — but the RECIPIENT must then raise
		// its own self-assessed output AND input tax legs, a second pair of
		// postings this generator does not derive. Emitting the invoice without
		// them would understate GSTR-3B liability while looking perfectly
		// balanced. An RCM item invoice is a tracked follow-up; until then it is
		// entered as a plain voucher with explicit postings.
		return errors.New("ledger: reverse-charge item invoices are not derived in v1 (the self-assessed tax legs are not generated); post the voucher with explicit postings")
	}
	return nil
}

// builtItemInvoice is what the generator produced, in the order the caller needs
// to use it: the rewritten stock lines, the derived GST lines, and the postings.
type builtItemInvoice struct {
	inventory []InventoryLine
	gstLines  []compliance.Line
	postings  []PostingInput
}

// buildItemInvoice turns the stock lines into the whole rest of the voucher.
// It runs INSIDE post()'s transaction because it reads the item masters, and
// the masters it reads must be the ones the movements are about to reference.
func buildItemInvoice(ctx context.Context, tx pgx.Tx, companyID uuid.UUID, in VoucherInput) (builtItemInvoice, error) {
	orient, err := orientationFor(in.Class)
	if err != nil {
		return builtItemInvoice{}, err
	}
	masters, err := loadItemMasters(ctx, tx, companyID, in.Inventory)
	if err != nil {
		return builtItemInvoice{}, err
	}

	// A FRESH slice. Mutating in.Inventory in place would reach through the
	// caller's own slice header and rewrite the amounts in the request object
	// they still hold — which for the sync sequencer (which retries an intent)
	// means the second attempt would carry values the client never sent.
	lines := make([]InventoryLine, len(in.Inventory))
	gstLines := make([]compliance.Line, len(in.Inventory))
	taxableTotal := decimal.Zero

	for i, l := range in.Inventory {
		m := masters[l.StockItemID]
		taxable := l.TaxableValue()

		// The line amount is DERIVED here, and a conflicting client value is
		// REFUSED rather than silently overwritten. Silently "correcting" a line
		// total is exactly the bug InventoryLine.Amount's comment guards
		// against: the printed invoice would then disagree with the stored one
		// and neither party would know which is real. Zero means "you compute
		// it", which is what a grid that only captures qty/rate/disc sends.
		if !l.Amount.IsZero() && !l.Amount.Equal(taxable) {
			return builtItemInvoice{}, fmt.Errorf(
				"ledger: inventory line %d (%s): amount %s contradicts qty x rate less discount = %s; an item invoice derives the line total",
				i+1, m.name, l.Amount.StringFixed(4), taxable.StringFixed(4))
		}

		l.Amount = taxable
		lines[i] = l
		gstLines[i] = compliance.Line{
			TaxableValue: taxable,
			GSTRatePct:   m.gstRate,
			HSN:          m.hsn,
		}
		taxableTotal = taxableTotal.Add(taxable)
	}

	// Derive the tax so the tax legs can be built. post() re-derives and
	// verifies these very postings a moment later — see the header for why that
	// second pass earns its keep.
	g := *in.GST
	g.Lines = gstLines
	tax, err := deriveGST(&g)
	if err != nil {
		return builtItemInvoice{}, err
	}

	// THE PARTY LEG: what the party owes (or is owed) is the value of the goods
	// PLUS the tax charged on them. taxableTotal is the sum of the per-line
	// DISCOUNTED values, which is the same number the movements and the GST
	// lines carry — the tie in migration 0027 is this equality, held by using
	// one variable.
	postings := []PostingInput{
		{LedgerID: in.PartyLedgerID, DrCr: orient.party, Amount: taxableTotal.Add(tax.TotalTax)},
		{LedgerID: in.ItemLedgerID, DrCr: orient.goods, Amount: taxableTotal},
	}

	// The tax legs. A zero component gets NO posting: an intra-state invoice has
	// no IGST at all, and a zero-rated item has none of the three, and recording
	// a 0.00 line for them is noise in every ledger printout. A NON-zero
	// component with no ledger named is a hard error — booking the party for tax
	// we cannot post anywhere would unbalance the voucher, and failing here says
	// WHY rather than leaving the double-entry check to complain about a number.
	taxLeg := func(name string, amount decimal.Decimal, id uuid.UUID) error {
		if amount.IsZero() {
			return nil
		}
		if id == uuid.Nil {
			return fmt.Errorf("ledger: item invoice derives %s of %s but no %s ledger was named",
				name, amount.StringFixed(4), name)
		}
		postings = append(postings, PostingInput{LedgerID: id, DrCr: orient.tax, Amount: amount})
		return nil
	}
	if err := taxLeg("CGST", tax.CGST, in.GST.CGSTLedgerID); err != nil {
		return builtItemInvoice{}, err
	}
	if err := taxLeg("SGST", tax.SGST, in.GST.SGSTLedgerID); err != nil {
		return builtItemInvoice{}, err
	}
	if err := taxLeg("IGST", tax.IGST, in.GST.IGSTLedgerID); err != nil {
		return builtItemInvoice{}, err
	}

	return builtItemInvoice{inventory: lines, gstLines: gstLines, postings: postings}, nil
}
