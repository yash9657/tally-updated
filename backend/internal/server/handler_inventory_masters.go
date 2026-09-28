package server

// handler_inventory_masters.go — the HTTP face of the inventory masters
// (migration 0025): godowns (stock locations) and batches/lots, plus the two
// READS that were missing for the stock masters that already had a POST.
//
// WHY THE READS MATTER AS MUCH AS THE WRITES: a client could already create a
// stock item and receive its id, but nothing could enumerate them — so a caller
// that did not create a master itself had no way to address it, including the
// 'Main Location' godown EVERY company is seeded with by
// seed_inventory_defaults. That is exactly the hole GET /ledgers was added to
// close for the chart of accounts, and it had the same consequence there:
// callers invented duplicates rather than reuse a row they could not name.
// An item grid cannot offer a location picker against a collection it cannot
// list.
//
// AUTHORIZATION: every path here is in isMasterPath, so routeAction maps the
// POSTs to (Create, "master") and the GETs to (Display, "master") — one ACL
// rule governs stock masters and accounting masters alike, and the seeded Data
// Entry level (which has `display master` from migration 0022 and no
// `create master`) can PICK a godown but not mint one.
//
// SAFETY: every read runs inside RunInReadOnlyTx — BEGIN ... READ ONLY with
// app.current_company_id bound as the first statement. godown, stock_batch,
// stock_item and unit are all FORCE RLS, so rows are tenant-scoped by the
// database rather than by the query text; none of these queries carries a
// company_id predicate and none of them needs one.

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/inventory"
	"github.com/khata/cored/internal/masters"
)

// --------------------------------------------------------------------------
// Godowns (stock locations)
// --------------------------------------------------------------------------

// godownReq is the POST /godowns body. There is deliberately NO is_system
// field: is_system marks the seeded 'Main Location' and is what a future delete
// path will refuse to act on, so letting a caller assert it would let a user
// mint an undeletable location.
type godownReq struct {
	Name      string `json:"name"`
	CreatedBy string `json:"created_by"`
}

func (s *Server) handleCreateGodown(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	var req godownReq
	if !decodeJSON(w, r, &req) {
		return
	}
	id, err := s.masters.CreateGodown(ctx, masters.Godown{Name: req.Name}, req.CreatedBy)
	if err != nil {
		writeMastersError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"godown_id": id.String()})
}

// godownListItem is one stock location. is_system is REPORTED (even though it
// cannot be set) so a client can tell the seeded 'Main Location' apart and
// default a new item's opening position to it rather than inventing a second
// location with the same meaning.
type godownListItem struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	IsSystem bool   `json:"is_system"`
}

func (s *Server) handleListGodowns(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	out := []godownListItem{}
	err := s.pool.RunInReadOnlyTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT id, name, is_system
			   FROM godown
			  ORDER BY name`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var it godownListItem
			if err := rows.Scan(&it.ID, &it.Name, &it.IsSystem); err != nil {
				return err
			}
			out = append(out, it)
		}
		return rows.Err()
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list godowns: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"godowns": out})
}

// --------------------------------------------------------------------------
// Batches / lots
// --------------------------------------------------------------------------

// stockBatchReq is the POST /batches body. stock_item_name is REQUIRED because
// batch names are ITEM-scoped, not company-scoped: two different items may each
// carry a batch called 'B-01' and they are unrelated. Collapsing them into one
// company-wide namespace would silently merge two physical batches with
// different expiry dates the first time a second supplier reuses a numbering
// scheme — which, for a pharma or food tenant, destroys the only thing batches
// exist to record.
//
// mfg_date / expiry_date are optional ISO dates; "" means "there isn't one".
// They are strings rather than times for the reason masters.StockBatch
// documents: a zero time.Time is a real instant (0001-01-01) and would make
// every undated batch look like the most expired stock in the warehouse.
type stockBatchReq struct {
	StockItemName string `json:"stock_item_name"`
	Name          string `json:"name"`
	MfgDate       string `json:"mfg_date"`
	ExpiryDate    string `json:"expiry_date"`
	CreatedBy     string `json:"created_by"`
}

func (s *Server) handleCreateStockBatch(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	var req stockBatchReq
	if !decodeJSON(w, r, &req) {
		return
	}
	id, err := s.masters.CreateStockBatch(ctx, masters.StockBatch{
		StockItemName: req.StockItemName,
		Name:          req.Name,
		MfgDate:       req.MfgDate,
		ExpiryDate:    req.ExpiryDate,
	}, req.CreatedBy)
	if err != nil {
		writeMastersError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"batch_id": id.String()})
}

// stockBatchListItem is one batch. It carries BOTH the item id and the item
// name: the id is what an inventory line must send, and the name is what makes
// the list groupable in a picker without a second lookup.
type stockBatchListItem struct {
	ID            string `json:"id"`
	StockItemID   string `json:"stock_item_id"`
	StockItemName string `json:"stock_item_name"`
	Name          string `json:"name"`
	// MfgDate/ExpiryDate are ISO dates, or "" when the batch carries none.
	MfgDate    string `json:"mfg_date"`
	ExpiryDate string `json:"expiry_date"`
}

func (s *Server) handleListStockBatches(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	out := []stockBatchListItem{}
	err := s.pool.RunInReadOnlyTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// to_char over the nullable DATEs renders an absent date as "" rather
		// than as a JSON null, matching the "" the POST body accepts, so a
		// client can read a batch and re-send it without a null-check.
		rows, err := tx.Query(ctx,
			`SELECT b.id, b.stock_item_id, si.name, b.name,
			        COALESCE(to_char(b.mfg_date,    'YYYY-MM-DD'), ''),
			        COALESCE(to_char(b.expiry_date, 'YYYY-MM-DD'), '')
			   FROM stock_batch b
			   JOIN stock_item si ON si.id = b.stock_item_id
			  ORDER BY si.name, b.name`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var it stockBatchListItem
			if err := rows.Scan(&it.ID, &it.StockItemID, &it.StockItemName, &it.Name,
				&it.MfgDate, &it.ExpiryDate); err != nil {
				return err
			}
			out = append(out, it)
		}
		return rows.Err()
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list batches: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"batches": out})
}

// --------------------------------------------------------------------------
// Stock items and units (the READ half of masters that already had a POST)
// --------------------------------------------------------------------------

// stockItemListItem is one inventory master. Field names mirror stockItemReq
// (the POST body) where they overlap, so a client can read an item and re-send
// it.
//
// THERE ARE NO OPENING FIGURES HERE, and their absence is the point. An opening
// position belongs to an (item, godown, batch) triple in stock_opening, not to
// the item — the same item legitimately opens with different quantities at
// different rates in two locations. Migration 0025 renamed the old flat columns
// to legacy_opening_qty/legacy_opening_rate precisely so that no reader could go
// on believing an item has one opening. Ask GET /stock-summary for a position.
type stockItemListItem struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// UnitSymbol is "" for an item with no unit set.
	UnitSymbol string `json:"unit_symbol"`
	HSN        string `json:"hsn"`
	// GSTRatePct is a fixed-4 decimal STRING, like every other rate on the wire.
	GSTRatePct string `json:"gst_rate_pct"`
	// ValuationMethod names the valuer that replays this item's movements
	// (WEIGHTED_AVG, FIFO or LIFO — T9), so a client can state a report's basis.
	ValuationMethod string `json:"valuation_method"`
	IsSystem        bool   `json:"is_system"`
	// StockGroup is the item's group (T10), or "" for an UNGROUPED item — which
	// is a real state and not a missing value: every item created before T10 is
	// ungrouped, and a picker that hid them would hide most of a company's stock.
	StockGroup string `json:"stock_group"`
}

// --------------------------------------------------------------------------
// STOCK GROUPS (T10, migration 0079)
// --------------------------------------------------------------------------

// stockGroupReq is the POST /stock-groups body. parent_name is optional and
// names an EXISTING group; a group is created under nothing by default, which is
// what Tally calls being under "Primary".
type stockGroupReq struct {
	Name       string `json:"name"`
	ParentName string `json:"parent_name"`
	CreatedBy  string `json:"created_by"`
}

func (s *Server) handleCreateStockGroup(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	var req stockGroupReq
	if !decodeJSON(w, r, &req) {
		return
	}
	id, err := s.masters.CreateStockGroup(ctx, masters.StockGroup{
		Name:       strings.TrimSpace(req.Name),
		ParentName: strings.TrimSpace(req.ParentName),
	}, req.CreatedBy)
	if err != nil {
		writeMastersError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"stock_group_id": id.String()})
}

// stockGroupListItem is one node of the item hierarchy.
//
// `depth` is served rather than left to the client to derive: it is the number
// of labels in the ltree path minus one, and a client that computed it by
// counting dots would be parsing a database detail it has no business knowing.
type stockGroupListItem struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	ParentName string `json:"parent_name"`
	Depth      int    `json:"depth"`
	// ItemCount is how many items sit DIRECTLY in this group — the one thing a
	// picker needs before it can offer to delete one.
	ItemCount int `json:"item_count"`
}

func (s *Server) handleListStockGroups(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	groups, err := s.masters.ListStockGroups(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list stock groups: "+err.Error())
		return
	}
	out := make([]stockGroupListItem, 0, len(groups))
	for _, g := range groups {
		out = append(out, stockGroupListItem{
			ID:         g.ID.String(),
			Name:       g.Name,
			ParentName: g.ParentName,
			Depth:      strings.Count(g.Path, "."),
			ItemCount:  g.ItemCount,
		})
	}
	// ⚠️ IN TREE ORDER (a parent always precedes its children), because that is
	// the order masters.ListStockGroups returns and re-sorting it here by name
	// would break the indentation a client renders from `depth`.
	writeJSON(w, http.StatusOK, map[string]any{"stock_groups": out})
}

func (s *Server) handleListStockItems(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	out := []stockItemListItem{}
	err := s.pool.RunInReadOnlyTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// LEFT JOIN: stock_item.unit_id is nullable, and an item without a unit
		// must still be pickable. An INNER JOIN here would make such an item
		// invisible to the grid while remaining postable — a master that exists
		// for the writer and not for the reader.
		rows, err := tx.Query(ctx,
			`SELECT si.id, si.name, COALESCE(u.symbol, ''), COALESCE(si.hsn, ''),
			        si.gst_rate_pct, si.valuation_method, si.is_system,
			        COALESCE(sg.name, '')
			   FROM stock_item si
			   LEFT JOIN unit u         ON u.id  = si.unit_id
			   LEFT JOIN stock_group sg ON sg.id = si.stock_group_id
			  ORDER BY si.name`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var (
				it   stockItemListItem
				rate decimal.Decimal
			)
			if err := rows.Scan(&it.ID, &it.Name, &it.UnitSymbol, &it.HSN,
				&rate, &it.ValuationMethod, &it.IsSystem, &it.StockGroup); err != nil {
				return err
			}
			it.GSTRatePct = rate.StringFixed(4)
			out = append(out, it)
		}
		return rows.Err()
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list stock items: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"stock_items": out})
}

// unitListItem is one unit of measure. decimal_places is what tells a grid how
// many decimals a quantity in this unit may carry (0 for "Nos", 3 for "Kg"), so
// it is part of the read even though nothing on the server enforces it yet.
type unitListItem struct {
	ID            string `json:"id"`
	Symbol        string `json:"symbol"`
	FormalName    string `json:"formal_name"`
	DecimalPlaces int    `json:"decimal_places"`
	IsSystem      bool   `json:"is_system"`
	// A COMPOUND unit (T10) is defined in terms of another: 1 BOX = 12 NOS. Both
	// are empty for a simple unit, and a client must render nothing rather than
	// "0.0000" in that case.
	//
	// ⚠️ REPORTED BUT NEVER APPLIED HERE. Quantities are stored in the item's own
	// unit and restated at REPORT time (the Stock Summary does it); a client that
	// multiplied a quantity by this factor itself would be doing the conversion
	// twice on any figure that had already been restated.
	BaseUnitSymbol   string `json:"base_unit_symbol"`
	ConversionFactor string `json:"conversion_factor"`
}

func (s *Server) handleListUnits(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	out := []unitListItem{}
	err := s.pool.RunInReadOnlyTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT u.id, u.symbol, u.formal_name, u.decimal_places, u.is_system,
			        COALESCE(b.symbol, ''), u.conversion_factor
			   FROM unit u
			   LEFT JOIN unit b ON b.id = u.base_unit_id
			  ORDER BY u.symbol`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var (
				it     unitListItem
				factor *decimal.Decimal
			)
			if err := rows.Scan(&it.ID, &it.Symbol, &it.FormalName,
				&it.DecimalPlaces, &it.IsSystem, &it.BaseUnitSymbol, &factor); err != nil {
				return err
			}
			// NULL stays "" rather than becoming "0.0000": a simple unit has NO
			// factor, and a zero one would read as "one BOX holds no pieces".
			if factor != nil {
				it.ConversionFactor = factor.StringFixed(4)
			}
			out = append(out, it)
		}
		return rows.Err()
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list units: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"units": out})
}

// --------------------------------------------------------------------------
// THE COSTING METHOD OF A STOCK ITEM (feature T9)
// --------------------------------------------------------------------------

// valuationMethodReq is the PUT /stock-items/{stockItemID}/valuation-method
// body. One field, and it is REQUIRED rather than a pointer — unlike the
// company settings PUT, where an absent field means "leave it alone". This
// route exists to change exactly one thing, so a body that names nothing is a
// caller mistake and not an instruction to do nothing.
type valuationMethodReq struct {
	ValuationMethod string `json:"valuation_method"`
	CreatedBy       string `json:"created_by"`
}

// handleSetStockItemValuationMethod switches one item's costing method.
//
// PUT /v1/companies/{companyID}/stock-items/{stockItemID}/valuation-method
//
// ⛔ THIS IS A RESTATEMENT AND THE ROUTE IS SHAPED LIKE ONE. Stock value is
// never stored, so changing the method changes NO data and every figure: the
// item's closing stock, its cost of goods sold, and therefore the Balance Sheet
// and the Profit & Loss for EVERY period the item has ever traded in — not just
// the ones after today, because the replay starts at the opening. So it is:
//
//   - its own narrow route rather than a field on a general master editor;
//   - an (Alter, "master") action, so it inherits the back-dated-entry cutoff
//     and the WebAuthn step-up like any other write (a change that restates a
//     closed period through the FRONT door should not walk past the lock that
//     stops a voucher doing it through the back one);
//   - refused when it names the method the item already has, so the audit trail
//     of restatements contains only real ones;
//   - answered WITH THE STORED ROW, never with the request body. That is
//     register row D43's lesson, and it is exactly the shape of failure this
//     route could have: a caller told the method changed while the column did
//     not move has no way to tell the two outcomes apart, and the next report
//     they read is on the old basis and looks perfectly plausible.
func (s *Server) handleSetStockItemValuationMethod(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	itemID, err := uuid.Parse(chi.URLParam(r, "stockItemID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "stockItemID is not a uuid")
		return
	}
	var req valuationMethodReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.ValuationMethod) == "" {
		writeError(w, http.StatusBadRequest,
			"valuation_method is required; GET ../inventory-vocabulary lists the methods this build values stock with")
		return
	}
	stored, err := s.masters.SetStockItemValuationMethod(ctx, itemID,
		strings.TrimSpace(req.ValuationMethod), attributedTo(r, req.CreatedBy))
	if err != nil {
		if errors.Is(err, masters.ErrValuationMethodUnchanged) {
			// 409, not 422: nothing about the request is unprocessable — the
			// world is already in the state it asks for.
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeMastersError(w, err)
		return
	}
	// The read-back, and the disclosure that goes with it. A client that shows
	// "saved" and nothing else would let an operator walk away not knowing that
	// last quarter's closing stock has just changed.
	writeJSON(w, http.StatusOK, map[string]any{
		"stock_item_id":    stored.StockItemID.String(),
		"name":             stored.Name,
		"valuation_method": stored.ValuationMethod,
		"restates": "Stock value is derived at report time, so this item's WHOLE history is " +
			"now valued on the new basis — including periods that have already been reported. " +
			"Switching back reproduces the earlier figures exactly.",
	})
}

// handleInventoryVocabulary serves the closed set of costing methods a stock
// item form must choose from, with Tally's own name for each and one line on
// what the closing stock ends up carried at.
//
// GET /v1/companies/{companyID}/inventory-vocabulary
//
// ⛔ SERVED RATHER THAN HARDCODED IN THE CLIENT, for the reason the payroll
// vocabulary is: a form built from a list the server does not own can compose a
// value the server refuses, and the operator meets that refusal after keying the
// whole master. The list is DERIVED from the implemented valuers
// (inventory.KnownValuationMethods), so a build that ships a method the reports
// cannot serve cannot offer it either.
//
// NO PARAMETERS, and nothing about this company in the answer: the vocabulary is
// this build's, not the tenant's. The company id is in the path only because
// every route on this seam is company-scoped by the middleware that authorises it.
func (s *Server) handleInventoryVocabulary(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.scopeCtx(w, r); !ok {
		return
	}
	methods := make([]map[string]any, 0, 3)
	for _, m := range inventory.MethodCatalogue() {
		methods = append(methods, map[string]any{
			"method": m.Method,
			"label":  m.Label,
			"basis":  m.Basis,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"valuation_methods": methods,
		"default":           inventory.MethodWeightedAverage,
	})
}
