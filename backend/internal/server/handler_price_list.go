package server

// handler_price_list.go — the HTTP face of the price-list masters (INV-L,
// migration 0030): create a named rate card, read one back, and — the endpoint
// that actually earns its keep — RESOLVE what we charge for one item, to one
// party, on one date, at one quantity.
//
// WHY THE RESOLVER IS A ROUTE AND NOT AN INTERNAL CALL. The rate a line is
// billed at has to be shown to the operator BEFORE they accept the voucher, and
// it has to be the same number the server will store. masters.ResolveRate
// rounds the net exactly once, at the scale of stock_movement.rate, so a grid
// that renders what this endpoint returns and then posts it cannot end up a
// paisa apart from the posting. A browser-side copy of the slab/window/party
// precedence would be a second implementation of the one thing in this feature
// that has to be deterministic.
//
// AUTHORIZATION: every path here is in isPriceListPath, so routeAction maps the
// GETs to (Display, "price-list") and the POST to (Create, "price-list") — its
// own resource token, not `master`. See authz/vocabulary.go for why, and
// migration 0031 for the judgement that token exists to express: the seeded
// 'Data Entry' level may READ the rate card (it is the number they must charge,
// and it is printed on every invoice they raise) and may not DEFINE one.
//
// A NOTE ON WHAT THIS SURFACE DOES NOT HAVE. There is no list-all route and no
// alter/delete: masters.Service has no method for either, and inventing one
// here would put price-list SQL in the HTTP layer, outside the transaction that
// appends the master_audit row. INV-L's handover records what an alter path
// must do (audit row in the same transaction; close the incumbent list's window
// in the same transaction as the new list's INSERT, or the pair deadlocks
// against the exclusion index under concurrency). Until that exists, a caller
// addresses a list by the id CreatePriceList returned.

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/masters"
)

// --------------------------------------------------------------------------
// POST /price-lists
// --------------------------------------------------------------------------

// priceListEntryReq is one item's rate within one list, for one quantity slab.
//
// QtyFrom is INCLUSIVE and QtyTo is EXCLUSIVE, and QtyToUnbounded is REQUIRED
// for the top slab. There is deliberately no "zero means unbounded" shorthand:
// zero is a real bound on the field beside it (qty_from: 0 is the ordinary
// first slab), and a shorthand meaning "nothing" in one field and "the bottom"
// in the next is how an off-by-one gets written into a price.
//
// rate and discount_pct are kept APART rather than pre-netted because the
// printed invoice has to show "1,000.00 less 5%", not "950.00" — a customer who
// negotiated a trade discount will ask where it went.
type priceListEntryReq struct {
	StockItemName  string `json:"stock_item_name"`
	QtyFrom        string `json:"qty_from"`
	QtyTo          string `json:"qty_to"`
	QtyToUnbounded bool   `json:"qty_to_unbounded"`
	Rate           string `json:"rate"`
	DiscountPct    string `json:"discount_pct"`
}

// priceListReq is the POST /price-lists body.
//
// effective_to is EXCLUSIVE and may be "" for an open-ended list ("these are
// our rates until we say otherwise"), which is the ordinary case.
// party_group_name narrows the list to one party group; "" means it applies to
// every party.
type priceListReq struct {
	Name           string              `json:"name"`
	PartyGroupName string              `json:"party_group_name"`
	EffectiveFrom  string              `json:"effective_from"`
	EffectiveTo    string              `json:"effective_to"`
	Entries        []priceListEntryReq `json:"entries"`
	CreatedBy      string              `json:"created_by"`
}

func (s *Server) handleCreatePriceList(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	var req priceListReq
	if !decodeJSON(w, r, &req) {
		return
	}

	pl := masters.PriceList{
		Name:           req.Name,
		PartyGroupName: req.PartyGroupName,
		EffectiveFrom:  req.EffectiveFrom,
		EffectiveTo:    req.EffectiveTo,
	}
	for i, e := range req.Entries {
		// Every numeric field goes through decimal parsing, never
		// strconv.ParseFloat: a float parser accepts "NaN" and "Inf", which the
		// decimal package then rejects by PANICKING, and a fat-fingered rate has
		// to come back as a 400 naming the entry rather than an opaque 500.
		//
		// qty_from is LENIENT ("" is zero) because the first slab genuinely
		// starts at nothing. rate is STRICT: "" is not "free", it is a rate
		// nobody entered, and a silent zero here bills the goods for nothing on
		// every voucher the list ever prices.
		where := fmt.Sprintf(" on entry %d", i+1)
		qtyFrom, err := optionalDecimal(e.QtyFrom)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid qty_from"+where)
			return
		}
		qtyTo, err := optionalDecimal(e.QtyTo)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid qty_to"+where)
			return
		}
		rate, err := decimal.NewFromString(e.Rate)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid rate"+where)
			return
		}
		discount, err := optionalDecimal(e.DiscountPct)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid discount_pct"+where)
			return
		}
		pl.Entries = append(pl.Entries, masters.PriceListEntry{
			StockItemName:  e.StockItemName,
			QtyFrom:        qtyFrom,
			QtyTo:          qtyTo,
			QtyToUnbounded: e.QtyToUnbounded,
			Rate:           rate,
			DiscountPct:    discount,
		})
	}

	id, err := s.masters.CreatePriceList(ctx, pl, req.CreatedBy)
	if err != nil {
		writeMastersError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"price_list_id": id.String()})
}

// --------------------------------------------------------------------------
// GET /price-lists/{priceListID}
// --------------------------------------------------------------------------

// handleGetPriceList reads one list's HEADER back — name, party-group scope and
// window. It deliberately carries NO rates: the entries are what ResolveRate
// answers with, one line at a time, and a bulk rate dump is a different
// disclosure from "what do we charge for this widget" (see migration 0031).
func (s *Server) handleGetPriceList(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "priceListID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid price list id")
		return
	}
	pl, err := s.masters.PriceListInfo(ctx, id)
	if err != nil {
		writeMastersError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"price_list_id": pl.ID.String(),
		"name":          pl.Name,
		// "" means the list applies to every party, matching the POST body.
		"party_group_name": pl.PartyGroupName,
		"effective_from":   pl.EffectiveFrom,
		// "" means open-ended, never "0001-01-01" — a zero date is one a report
		// would happily believe.
		"effective_to": pl.EffectiveTo,
	})
}

// --------------------------------------------------------------------------
// GET /price-lists/quote
// --------------------------------------------------------------------------

// handlePriceQuote answers ONE PriceQuery: what do we charge for this item, to
// this party group, on this date, at this quantity.
//
// EVERY PARAMETER IS A QUERY PARAM AND on/qty ARE BOTH REQUIRED.
//
//   - `on` is required because a rate is only meaningful as at a day: a list
//     has a window, and defaulting to today would price a back-dated voucher
//     from a season that had not happened when it was raised.
//   - `qty` is required for the reason that is easy to get wrong. Slabs exist
//     precisely to price BY quantity, and the first slab starts at zero — so an
//     omitted quantity silently resolving to 0 would answer with the SMALLEST
//     slab, which is normally the dearest rate. A wholesale order quoted at the
//     retail rate is a plausible, confident, wrong answer, and nothing
//     downstream could detect it.
//
// party_group_id is optional: uuid.Nil means the caller has no party in hand and
// only the company-wide list is considered. The group is matched EXACTLY — the
// account-group tree is deliberately not walked, so re-parenting a debtor group
// can never silently re-price them.
func (s *Server) handlePriceQuote(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()

	itemID, err := uuid.Parse(q.Get("stock_item_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid or missing stock_item_id")
		return
	}
	groupID, err := optionalUUID(q.Get("party_group_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid party_group_id")
		return
	}
	on := q.Get("on")
	if on == "" {
		writeError(w, http.StatusBadRequest,
			"on is required: a rate is only meaningful as at a day, because a price list has a window")
		return
	}
	rawQty := q.Get("qty")
	if rawQty == "" {
		writeError(w, http.StatusBadRequest,
			"qty is required: rates are banded by quantity, and an assumed quantity would quote the "+
				"smallest slab — normally the dearest rate — for an order that had earned a better one")
		return
	}
	qty, err := decimal.NewFromString(rawQty)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid qty")
		return
	}

	quote, err := s.masters.ResolveRate(ctx, masters.PriceQuery{
		StockItemID:  itemID,
		PartyGroupID: groupID,
		On:           on,
		Qty:          qty,
	})
	if err != nil {
		// ErrNoRate IS NOT A ZERO RATE and it is not a 404 either. Every master
		// named exists; what is missing is a slab or a window that covers this
		// day and quantity, and the remedy ("widen the window, add a slab") is a
		// different action from "create the master". It comes back as a 422 with
		// a machine-readable code so a grid can put the cursor in the rate field
		// and let the operator key one, rather than silently billing zero — zero
		// is a LEGAL price (free samples are real), so a caller that could not
		// tell the two apart would invoice goods for nothing and still balance.
		if errors.Is(err, masters.ErrNoRate) {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{
				"error": err.Error(),
				"code":  "NO_RATE",
			})
			return
		}
		writeMastersError(w, err)
		return
	}

	// rate/discount_pct/net_rate are all carried. The invoice prints the gross
	// and the discount; the movement stores the net. Rounding happened ONCE,
	// inside ResolveRate, at the scale of stock_movement.rate — so the number
	// quoted here is byte-for-byte the number the posting will hold, and a
	// client must never re-derive the net from the other two.
	out := map[string]any{
		"price_list_id":   quote.PriceListID.String(),
		"price_list_name": quote.PriceListName,
		// party_scoped records WHICH precedence level answered. "Why was this
		// customer charged less than that one" is a question somebody
		// eventually asks, and it is far cheaper to answer from a stored flag
		// than by re-deriving the price lists as they stood on the voucher date.
		"party_scoped":     quote.PartyScoped,
		"rate":             quote.Rate.StringFixed(4),
		"discount_pct":     quote.DiscountPct.StringFixed(4),
		"net_rate":         quote.NetRate.StringFixed(4),
		"qty_from":         quote.QtyFrom.StringFixed(4),
		"qty_to_unbounded": quote.QtyToUnbounded,
	}
	// qty_to is MEANINGLESS on the top slab, so it is omitted rather than sent
	// as 0 — a 0 upper bound reads as a legitimate (and absurd) band.
	if !quote.QtyToUnbounded {
		out["qty_to"] = quote.QtyTo.StringFixed(4)
	}
	writeJSON(w, http.StatusOK, out)
}
