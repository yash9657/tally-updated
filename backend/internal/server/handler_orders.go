package server

// handler_orders.go — sales/purchase orders and the bill of materials over HTTP
// (T3, migrations 0042 and 0043).
//
//	POST   .../sales-orders            -> Create / sales-order
//	GET    .../sales-orders            -> Display / sales-order
//	GET    .../sales-orders/{id}       -> Display / sales-order
//	PUT    .../sales-orders/{id}/close -> Alter / sales-order
//	... and the same four for purchase-orders, and for boms:
//	POST   .../boms                    -> Create / bom
//	GET    .../boms                    -> Display / bom
//	GET    .../boms/{id}               -> Display / bom
//	GET    .../boms/{id}/explode?qty=  -> Display / bom
//
// ===========================================================================
// TWO ROUTE FAMILIES AND TWO TOKENS, OVER ONE TABLE
// ===========================================================================
// The schema is ONE order_head with a direction column (migration 0042: two head
// tables mean two copies of every constraint). The ROUTES split, because
// `sales-order` and `purchase-order` are separate authorities: a salesperson keys
// what customers want and a purchase clerk commits the company to spending, and
// "may key sales orders, may not commit us to paying suppliers" is a rule a
// business actually writes. `routeAction` emits the token from the PATH, so the
// split has to be in the path.
//
// ⚠️ Contrast `bom`, which is ONE token for the recipe and its components: a
// component's bom_id is NOT NULL, so the two are one object and two tokens would
// have to be granted together. The test recorded in authz/vocabulary.go is not
// "are these things similar" but "would an administrator ever grant one without
// the other".
//
// ===========================================================================
// ⛔ NONE OF THESE IS A REPORT, AND NONE MAY BE ADDED TO KnownReportNames
// ===========================================================================
// server.go loops over that list and auto-mounts GET /print/reports/<name>; a name
// with no reportName() arm is a route routeAction cannot map, i.e. default-DENY for
// everyone including the Owner. That bug already shipped once for
// reconciliation-queue. The two order BOOKS are report names and are registered as
// such (handler_orderbook.go); these are the MASTERS routes and are not them.
//
// ===========================================================================
// WHY CLOSING AN ORDER IS ITS OWN ROUTE AND AN Alter
// ===========================================================================
// `PUT .../{id}/close` rather than a PATCH on the order body, because closing is
// the ONE mutable fact about an order that is not derived — a human decision to
// abandon it — and it carries a REQUIRED reason (0042 CHECKs that a closed order
// has one and an open one does not). A general-purpose update route would let a
// caller alter the commitment and the abandonment in one request and leave the
// server deciding which the operator meant.
//
// It is an Alter and not a Delete: the order stays on the books with its reason,
// because an order that vanished would take the question "what happened to SO-14"
// with it.
//
// QUANTITIES AND MONEY GO OUT AS FIXED-4 STRINGS. A JSON number is a float64 and a
// commitment is not a thing to send through one.

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/orders"
)

type orderLineReq struct {
	StockItemID string `json:"stock_item_id"`
	Qty         string `json:"qty"`
	Rate        string `json:"rate"`
	DueDate     string `json:"due_date"`
}

type orderReq struct {
	OrderNo string `json:"order_no"`
	// OrderDate is the date the commitment was made. ⚠️ NOT the due date: an order
	// placed in June for delivery in September is dated June, and the order book's
	// as-at walk bounds on this.
	OrderDate     string         `json:"order_date"`
	PartyLedgerID string         `json:"party_ledger_id"`
	Narration     string         `json:"narration"`
	CreatedBy     string         `json:"created_by"`
	Lines         []orderLineReq `json:"lines"`
	// ⛔ NO `direction` FIELD, DELIBERATELY. The ROUTE carries it, because
	// routeAction reads the path to decide whether this is `sales-order` or
	// `purchase-order` authority. A body field would let a caller authorised for
	// sales orders POST a purchase commitment to the sales route and have it
	// accepted — the PDP would have said yes to the wrong object.
}

// closeOrderReq is the abandonment. `is_closed: false` clears it, which is the
// only reading under which "I closed the wrong order" is correctable.
type closeOrderReq struct {
	IsClosed bool   `json:"is_closed"`
	Reason   string `json:"reason"`
}

// postOrder returns a handler bound to a fixed direction. The DIRECTION COMES
// FROM THE ROUTE — see orderReq.
func (s *Server) postOrder(direction orders.Direction) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, ok := s.scopeCtx(w, r)
		if !ok {
			return
		}
		var req orderReq
		if !decodeJSON(w, r, &req) {
			return
		}
		in := orders.Input{
			Direction: direction,
			OrderNo:   req.OrderNo,
			OrderDate: req.OrderDate,
			Narration: req.Narration,
			CreatedBy: req.CreatedBy,
		}
		var err error
		// ⛔ THE ZERO UUID PARSES (HANDOFF §7.3). optionalUUID would turn "" into
		// uuid.Nil and the domain refuses that with a sentence, so the parse only has
		// to catch garbage; an explicit all-zeros is refused by the same domain check.
		if in.PartyLedgerID, err = optionalUUID(req.PartyLedgerID); err != nil {
			writeError(w, http.StatusBadRequest, "party_ledger_id must be the uuid of a party ledger")
			return
		}
		for i, l := range req.Lines {
			where := " on line " + strconv.Itoa(i+1)
			itemID, perr := optionalUUID(l.StockItemID)
			if perr != nil {
				writeError(w, http.StatusBadRequest, "stock_item_id must be a uuid"+where)
				return
			}
			// decimal.NewFromString, never ParseFloat: float parsing accepts "NaN" and
			// "Inf" (which panic inside the decimal package as an opaque 500) and would
			// silently round a 19-digit quantity, so the commitment stored would not be
			// the one keyed.
			qty, perr := decimal.NewFromString(strings.TrimSpace(l.Qty))
			if perr != nil {
				writeError(w, http.StatusBadRequest,
					"qty must be a decimal number — the quantity COMMITTED, which is not a movement"+where)
				return
			}
			rate := decimal.Zero
			if strings.TrimSpace(l.Rate) != "" {
				if rate, perr = decimal.NewFromString(strings.TrimSpace(l.Rate)); perr != nil {
					writeError(w, http.StatusBadRequest, "rate must be a decimal number"+where)
					return
				}
			}
			in.Lines = append(in.Lines, orders.LineInput{
				StockItemID: itemID,
				Qty:         qty,
				Rate:        rate,
				// "" stays "" and means NO DATE AGREED, which is not the same as
				// overdue. The domain writes NULL and the order book never treats it as
				// a missed promise.
				DueDate: strings.TrimSpace(l.DueDate),
			})
		}

		orderID, err := s.orders.Create(ctx, in)
		if err != nil {
			writeOrderError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"order_id": orderID.String()})
	}
}

// getOrders lists one direction's orders. `?open_only=true` filters to orders with
// something still outstanding, which is what a fulfilment picker wants.
func (s *Server) getOrders(direction orders.Direction) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, ok := s.scopeCtx(w, r)
		if !ok {
			return
		}
		openOnly := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("open_only")), "true")
		list, err := s.orders.List(ctx, direction, openOnly)
		if err != nil {
			writeOrderError(w, err)
			return
		}
		rows := make([]map[string]any, 0, len(list))
		for _, o := range list {
			rows = append(rows, orderHeadJSON(o))
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"direction": string(direction),
			"orders":    rows,
			// Echoed so a screen cannot render a filtered list under an unfiltered
			// heading — "no orders" and "no OPEN orders" are different answers.
			"open_only": openOnly,
		})
	}
}

func (s *Server) getOrder(direction orders.Direction) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, ok := s.scopeCtx(w, r)
		if !ok {
			return
		}
		orderID, ok := pathUUID(w, r, "orderID", "order id")
		if !ok {
			return
		}
		o, err := s.orders.Get(ctx, orderID)
		if err != nil {
			writeOrderError(w, err)
			return
		}
		// ⛔ THE DIRECTION IS CHECKED AGAINST THE ROUTE, not merely echoed. Both
		// families read one table, so GET /sales-orders/{id-of-a-purchase-order}
		// would otherwise serve a purchase commitment to a caller who holds only
		// `sales-order` — the PDP allowed the route, and the row is the other object.
		// A 404 and not a 403: the resource does not exist AT THIS PATH, and saying
		// "forbidden" would confirm the id names something.
		if o.Direction != direction {
			writeError(w, http.StatusNotFound, "no such "+strings.ToLower(string(direction))+" order")
			return
		}
		body := orderHeadJSON(o)
		lines := make([]map[string]any, 0, len(o.Lines))
		for _, l := range o.Lines {
			lines = append(lines, map[string]any{
				"order_line_id": l.ID.String(),
				"line_no":       l.LineNo,
				"stock_item_id": l.StockItemID.String(),
				"stock_item":    l.StockItem,
				"qty":           l.Qty.StringFixed(4),
				"rate":          l.Rate.StringFixed(4),
				// "" when no date was agreed — never a zero date, which a client would
				// render as the year 1.
				"due_date": l.DueDate,
				// ⛔ DERIVED, NOT STORED. Summed from order_fulfilment across CURRENT,
				// non-deleted voucher versions on every read. A client must not cache
				// these across a voucher edit.
				"fulfilled_qty":   l.Fulfilled.StringFixed(4),
				"outstanding_qty": l.Outstanding().StringFixed(4),
			})
		}
		body["lines"] = lines
		writeJSON(w, http.StatusOK, body)
	}
}

func (s *Server) putOrderClose(direction orders.Direction) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, ok := s.scopeCtx(w, r)
		if !ok {
			return
		}
		orderID, ok := pathUUID(w, r, "orderID", "order id")
		if !ok {
			return
		}
		var req closeOrderReq
		if !decodeJSON(w, r, &req) {
			return
		}
		// The direction guard, for the reason getOrder records.
		o, err := s.orders.Get(ctx, orderID)
		if err != nil {
			writeOrderError(w, err)
			return
		}
		if o.Direction != direction {
			writeError(w, http.StatusNotFound, "no such "+strings.ToLower(string(direction))+" order")
			return
		}
		if err := s.orders.Close(ctx, orderID, req.IsClosed, req.Reason, ""); err != nil {
			writeOrderError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"order_id":  orderID.String(),
			"is_closed": req.IsClosed,
		})
	}
}

func orderHeadJSON(o orders.Order) map[string]any {
	return map[string]any{
		"order_id":        o.ID.String(),
		"direction":       string(o.Direction),
		"order_no":        o.OrderNo,
		"order_date":      o.OrderDate,
		"party_ledger_id": o.PartyLedgerID.String(),
		"party":           o.Party,
		"narration":       o.Narration,
		// ⚠️ MANUAL ABANDONMENT, NOT A FULFILMENT STATE. A screen that labelled this
		// "closed" beside a fully-delivered order would conflate two different facts:
		// fulfilment is derived and can never be asserted.
		"is_closed":     o.IsClosed,
		"closed_reason": o.ClosedReason,
		"created_by":    o.CreatedBy,
	}
}

// ---------------------------------------------------------------------------
// The bill of materials
// ---------------------------------------------------------------------------

type bomComponentReq struct {
	StockItemID string `json:"stock_item_id"`
	Qty         string `json:"qty"`
}

type bomReq struct {
	StockItemID string `json:"stock_item_id"`
	Name        string `json:"name"`
	// OutputQty is the YIELD — how many of the finished good this recipe makes.
	// ⛔ The components are stored AGAINST it and are never pre-divided per unit:
	// a 10kg-makes-3 recipe normalised to 3.3333/unit truncates at NUMERIC(19,4)
	// and shorts every batch forever. Send the recipe as written.
	OutputQty  string            `json:"output_qty"`
	IsDefault  bool              `json:"is_default"`
	CreatedBy  string            `json:"created_by"`
	Components []bomComponentReq `json:"components"`
}

func (s *Server) postBom(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	var req bomReq
	if !decodeJSON(w, r, &req) {
		return
	}
	in := orders.BomInput{
		Name:      req.Name,
		IsDefault: req.IsDefault,
		CreatedBy: req.CreatedBy,
	}
	var err error
	if in.StockItemID, err = optionalUUID(req.StockItemID); err != nil {
		writeError(w, http.StatusBadRequest,
			"stock_item_id must be the uuid of the FINISHED GOOD this recipe produces")
		return
	}
	if in.OutputQty, err = decimal.NewFromString(strings.TrimSpace(req.OutputQty)); err != nil {
		writeError(w, http.StatusBadRequest,
			"output_qty must be a decimal number — the YIELD of this recipe, which the components are "+
				"stated against and are never divided by")
		return
	}
	for i, c := range req.Components {
		where := " on component " + strconv.Itoa(i+1)
		itemID, perr := optionalUUID(c.StockItemID)
		if perr != nil {
			writeError(w, http.StatusBadRequest, "stock_item_id must be a uuid"+where)
			return
		}
		qty, perr := decimal.NewFromString(strings.TrimSpace(c.Qty))
		if perr != nil {
			writeError(w, http.StatusBadRequest,
				"qty must be a decimal number — what the recipe consumes to yield output_qty of the "+
					"finished good, NOT per unit of it"+where)
			return
		}
		in.Components = append(in.Components, orders.ComponentInput{StockItemID: itemID, Qty: qty})
	}

	bomID, err := s.orders.CreateBom(ctx, in)
	if err != nil {
		writeOrderError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"bom_id": bomID.String()})
}

func (s *Server) getBoms(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	var forItem uuid.UUID
	if raw := strings.TrimSpace(r.URL.Query().Get("stock_item_id")); raw != "" {
		id, err := uuid.Parse(raw)
		// ⛔ AN EXPLICIT ALL-ZEROS UUID IS REFUSED (HANDOFF §7.3). uuid.Nil is this
		// filter's "no filter" sentinel, so an all-zeros id would silently return
		// EVERY recipe in the company — a 200 with a truthful-looking body answering
		// a question nobody asked. That exact defect shipped in T2's cost-centre
		// filter.
		if err != nil || id == uuid.Nil {
			writeError(w, http.StatusBadRequest,
				"stock_item_id must be the uuid of a stock item; omit it entirely to list every recipe")
			return
		}
		forItem = id
	}
	list, err := s.orders.ListBoms(ctx, forItem)
	if err != nil {
		writeOrderError(w, err)
		return
	}
	rows := make([]map[string]any, 0, len(list))
	for _, b := range list {
		rows = append(rows, map[string]any{
			"bom_id":        b.ID.String(),
			"stock_item_id": b.StockItemID.String(),
			"stock_item":    b.StockItem,
			"name":          b.Name,
			"output_qty":    b.OutputQty.StringFixed(4),
			"is_default":    b.IsDefault,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"boms": rows})
}

func (s *Server) getBom(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	bomID, ok := pathUUID(w, r, "bomID", "bom id")
	if !ok {
		return
	}
	b, err := s.orders.GetBom(ctx, bomID)
	if err != nil {
		writeOrderError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, bomJSON(b))
}

// getBomExplode scales a recipe to a wanted quantity and returns what a Stock
// Journal's lines would be.
//
// A GET and not a POST: it writes nothing. ⛔ AND IT DOES NOT POST THE JOURNAL —
// the operator reviews the explosion and submits it as an ordinary Stock Journal,
// which is what keeps a manufacture an auditable voucher rather than a side effect
// of opening a recipe. internal/orders could not post it in any case; archtest
// forbids the edge to internal/ledger.
func (s *Server) getBomExplode(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	bomID, ok := pathUUID(w, r, "bomID", "bom id")
	if !ok {
		return
	}
	raw := strings.TrimSpace(r.URL.Query().Get("qty"))
	if raw == "" {
		writeError(w, http.StatusBadRequest,
			"qty is required — it is how many of the finished good to make, and the components are "+
				"scaled by (qty / the recipe's yield). There is no sensible default: guessing 1 would "+
				"quietly answer a question about a different batch size")
		return
	}
	wanted, err := decimal.NewFromString(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "qty must be a decimal number")
		return
	}
	ex, err := s.orders.Explode(ctx, bomID, wanted)
	if err != nil {
		writeOrderError(w, err)
		return
	}
	comps := make([]map[string]any, 0, len(ex.Components))
	for _, c := range ex.Components {
		comps = append(comps, map[string]any{
			"line_no":       c.LineNo,
			"stock_item_id": c.StockItemID.String(),
			"stock_item":    c.StockItem,
			"qty":           c.Qty.StringFixed(4),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"bom_id":     ex.BomID.String(),
		"stock_item": ex.StockItem,
		"wanted_qty": ex.WantedQty.StringFixed(4),
		// The recipe's own yield, echoed so a screen can show the ratio it scaled by
		// rather than leaving an operator to wonder why 3 units consumed 10 kg.
		"output_qty": ex.OutputQty.StringFixed(4),
		"components": comps,
		// ⛔ RENDER THESE. They state that the explosion is SINGLE-LEVEL and that
		// nothing has moved yet. A screen that implied the explosion reached raw
		// materials would have an operator issuing components they do not hold.
		"notes": ex.Notes,
	})
}

func bomJSON(b orders.Bom) map[string]any {
	comps := make([]map[string]any, 0, len(b.Components))
	for _, c := range b.Components {
		comps = append(comps, map[string]any{
			"line_no":       c.LineNo,
			"stock_item_id": c.StockItemID.String(),
			"stock_item":    c.StockItem,
			"qty":           c.Qty.StringFixed(4),
		})
	}
	return map[string]any{
		"bom_id":        b.ID.String(),
		"stock_item_id": b.StockItemID.String(),
		"stock_item":    b.StockItem,
		"name":          b.Name,
		"output_qty":    b.OutputQty.StringFixed(4),
		"is_default":    b.IsDefault,
		"components":    comps,
	}
}

// writeOrderError maps the package's sentinels onto status codes.
//
// The mapping is the point: ErrConflict is a 409 because the operator can fix it
// by choosing another name, where a 400 would say "your request is malformed" when
// it is not and a 500 would say "we broke". ErrStillFulfilled is a 422 because the
// request is well-formed and the BOOKS refuse it.
func writeOrderError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, orders.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, orders.ErrConflict):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	}
}

// pathUUID parses a {name} path parameter, writing a 400 on failure.
func pathUUID(w http.ResponseWriter, r *http.Request, param, label string) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, param))
	if err != nil || id == uuid.Nil {
		writeError(w, http.StatusBadRequest, "invalid "+label)
		return uuid.Nil, false
	}
	return id, true
}
