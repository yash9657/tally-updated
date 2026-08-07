package server

// handler_voucher_inventory.go — the HTTP shape of STOCK ON A VOUCHER.
//
// Two halves, and the pair of them is the whole contract:
//
//   - what a client SENDS: voucherReq.Inventory (+ item_ledger_id) -> the
//     ledger's VoucherInput, built in buildVoucherInput/buildInventoryLine; and
//   - what a client READS BACK: inventoryLinesJSON, the `inventory` array on
//     GET /vouchers/{id}.
//
// THE INVARIANT THIS FILE EXISTS TO HOLD: the two halves must be the SAME shape,
// field for field, because an ALTER is "fetch it, change one thing, send it all
// back". A voucher is restated in full on every alter — the ledger appends a new
// version built ONLY from what the request carried — so any part of the voucher
// the read shape forgets to project is a part the next alter DELETES. For stock
// that failure is silent and unbounded: the accounting legs come back untouched,
// the trial balance is unchanged, the voucher looks edited-and-fine, and the
// goods have simply left the movement ledger. Nothing in the books is out of
// balance afterwards, so no report ever complains. That is what
// TestAlterViaHTTPPreservesStock is for, and why it PUTs the fetched body back
// verbatim instead of a hand-written one: a hand-written body would be testing
// the test author's memory of the shape rather than the shape itself.
//
// THE ITEM-INVOICE REQUEST (F8/F9 item grid), for the record, since this is the
// file a future reader will open to find it:
//
//	POST /v1/companies/{id}/vouchers/sales      (or /purchase, /credit-note, /debit-note)
//	{
//	  "party_ledger_id": "<uuid>",           // REQUIRED: customer (Sales/Credit Note) or supplier
//	  "item_ledger_id":  "<uuid>",           // REQUIRED: the Sales/Purchase Accounts ledger
//	  "voucher_date": "2025-05-10", "voucher_no": "",   // "" => the server allocates
//	  "inventory": [{"stock_item_id":"<uuid>","godown_id":"<uuid or empty>","batch_id":"",
//	                 "direction":"OUT","qty":"10.0000","rate":"100.0000","discount_pct":"10.0000"}],
//	  "gst": {"place_of_supply_state_code":"27","cgst_ledger_id":"<uuid>",
//	          "sgst_ledger_id":"<uuid>","igst_ledger_id":"<uuid>"}
//	}
//
// and the three things that make it an ITEM invoice rather than an accounting
// one, all enforced by internal/ledger and none of them re-checked here:
//
//   - NO `postings` and NO `gst.lines`. Both are GENERATED from the stock lines;
//     sending a second, independent set is a 422 (ErrInventoryAndPostings /
//     ErrInventoryAndGSTLines), never a merge. HSN and GST rate come from the
//     stock item master — a client cannot type them onto a voucher at all.
//   - `direction` is fixed by the class: Sales/Debit Note move stock OUT,
//     Purchase/Credit Note move it IN. A mismatch is a 422 naming the line.
//   - `amount` per line is optional: omit it (or send "") and the server derives
//     qty x rate less discount. Send a CONTRADICTING one and the post is refused
//     rather than silently corrected, so a client-side rounding drift surfaces on
//     the first discounted line instead of as a paisa on every invoice.
//     ⚠️ THAT LAST RULE IS NO LONGER ITEM-INVOICE-ONLY. ledger.validateInventory
//     applies it to EVERY class that carries a grid (and to the sync/import doors
//     with it): a stated amount that contradicts qty x rate less discount is a
//     422. It has to be — stock VALUE is replayed from qty/rate/discount and the
//     amount column is read back by nothing, so an inconsistent pair puts one
//     number on the printed document and a different one on the Balance Sheet.
//
// The response is the ordinary voucher result (voucher_id, version_no,
// voucher_no, cgst/sgst/igst): the tax on it is the SERVER-derived tax, so an
// F8 screen should print its invoice total from the response rather than from
// its own arithmetic.

import "github.com/khata/cored/internal/ledger"

// inventoryLinesJSON serialises the stock lines of a fetched voucher into the
// `inventory` array of GET /vouchers/{id}.
//
// FIELD NAMES MIRROR inventoryLineReq (the POST/PUT shape) so a client can fetch
// a voucher and re-POST it verbatim on an alter; the extra display-only keys
// (line_no, item, godown, batch names) are ignored by the decoder on the way
// back in, which is what lets the round trip be a plain echo.
//
// THREE THINGS ARE LOAD-BEARING, and each one has cost this codebase a bug in a
// neighbouring layer:
//
//  1. THE UUIDS. item/godown/batch are projected as ids, not just names.
//     Re-resolving a line by NAME on the way back would drop any line whose item
//     was renamed between the fetch and the save, turning an edit of the
//     narration into an inventory write-off. (uuidOrEmpty renders an absent
//     optional dimension as "" — the same token the request side reads as "not
//     tracked on this line" — rather than as the all-zero uuid, which would come
//     back as a 400 on the next PUT.)
//
//  2. FIXED-4 STRINGS, NEVER JSON NUMBERS, for qty/rate/discount_pct/amount. A
//     JSON number is a float64 in every client that parses it, and a float
//     quantity makes a closing stock balance depend on the order the lines were
//     entered; the value the report derives would then differ from the value the
//     invoice printed. Quantities are money here. This is also why the request
//     side parses them with decimal.NewFromString and not ParseFloat.
//
//  3. THE RATE, WHICH IS THE FIELD THE MONEY HANGS ON. Closing stock value is
//     REPLAYED from qty x rate less discount (internal/inventory's valuer) and is
//     never read out of stock_movement.amount, so a projection that returns the
//     grid without a usable price is not a cosmetic loss: the alter rebuilt from
//     it restates every unit at no cost and closing stock — a BALANCE SHEET line —
//     falls to zero with the quantity still correct and nothing out of balance.
//     A quantity-only round-trip test cannot see it; both TestAlterViaHTTPPreserves
//     Stock and the read-back audit therefore assert closing_value as well.
func inventoryLinesJSON(lines []ledger.InventoryLineView) []map[string]any {
	out := make([]map[string]any, 0, len(lines))
	for _, l := range lines {
		out = append(out, map[string]any{
			"line_no":       l.LineNo,
			"stock_item_id": l.StockItemID.String(),
			"item":          l.ItemName,
			"godown_id":     uuidOrEmpty(l.GodownID),
			"godown":        l.GodownName,
			"batch_id":      uuidOrEmpty(l.BatchID),
			"batch":         l.BatchName,
			"direction":     l.Direction,
			"qty":           l.Qty.StringFixed(4),
			"rate":          l.Rate.StringFixed(4),
			"discount_pct":  l.DiscountPct.StringFixed(4),
			"amount":        l.Amount.StringFixed(4),
		})
	}
	return out
}
