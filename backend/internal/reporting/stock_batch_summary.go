package reporting

// stock_batch_summary.go — BATCH-WISE STOCK WITH EXPIRY AGEING.
//
// This is the report a pharma, food or chemicals tenant actually opens: not
// "how much do we own" but "how much of what we own is about to become
// unsellable, and when". It is the batch dimension of the same replayed
// position stock_summary.go produces — same loaders, same valuers, same
// current-version join — with each batch classified into ONE ageing band
// relative to the report's as-on date.
//
// EXPIRED STOCK IS FLAGGED, NEVER EXCLUDED
// ----------------------------------------
// This is the load-bearing rule of the whole file and it is the one a
// well-meaning `WHERE expiry_date >= $1` destroys in a single line.
//
// An expired batch is STILL PHYSICALLY IN THE WAREHOUSE. It is still the
// company's property, it is still on the Balance Sheet at cost until somebody
// writes it off with a voucher, and the reason to print this report at all is to
// see it. A report that filtered it out would show a smaller, tidier, entirely
// fictional inventory: the closing quantity would silently drop, the batch rows
// would stop adding up to the item total the Stock Summary prints, and the day
// closing stock is folded into the Balance Sheet the same filter would quietly
// understate the assets of the business. A write-off is a VOUCHER — a decision
// somebody makes and signs — not a WHERE clause in a report.
//
// So expiry here is a CLASSIFICATION, never a filter:
// TestExpiredStockStillCountsInClosingQuantity pins that the expired batch's
// quantity is present in the item total, and the flag is what the UI colours
// red.
//
// THE BANDS AND THEIR BOUNDARIES
// ------------------------------
// Relative to as-on date D, for a batch expiring on E:
//
//	E is unknown  -> NO_EXPIRY       (a batch, or a position, that tracks none)
//	E <  D        -> EXPIRED
//	E <= D + 30   -> DUE_IN_30_DAYS
//	E <= D + 90   -> DUE_IN_90_DAYS
//	otherwise     -> BEYOND_90_DAYS
//
// Every boundary is INCLUSIVE on the nearer band, exactly as every date bound in
// this package is inclusive on both ends. A batch expiring EXACTLY 30 days out
// is due within 30 days; flipping one `<=` to `<` moves it into the next band
// out, which is how a stock controller comes to be told that a batch is safe for
// three months on the last day it could have been sold.
//
// A batch expiring exactly ON the as-on date is NOT yet expired — it is due
// today, in the 30-day band. Stock is sellable through its expiry date, and
// stranding a day's worth of saleable inventory in a red row is a real cost to a
// real business. The morning after, the same batch is EXPIRED.
//
// UNITS, AND WHY A BAND HAS NO QUANTITY
// -------------------------------------
// A band spans items, so it carries VALUE only. Adding kilograms of one item to
// litres of another produces a number with no unit and no meaning — the same
// reason StockSummary has a TotalValue and no total quantity. Quantities live on
// the rows and on the per-ITEM rollups, where a unit exists.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/inventory"
)

// isoDate is the one date layout this whole seam speaks.
const isoDate = "2006-01-02"

// ErrAgeingNeedsAsOn is returned when a batch ageing report is asked for with no
// as-on date.
//
// Every other report in this package accepts an empty date to mean "everything
// ever posted", and an ageing report cannot: a band is defined RELATIVE to a
// date, so without one there is nothing to be 30 days away from. Defaulting to
// time.Now() was the alternative and it is worse — it would make the report
// non-reproducible (the same books printed on two days give different bands with
// no column saying why) and would put a clock inside a derivation that is
// otherwise a pure function of the books. The caller decides what "today" means
// and says so.
var ErrAgeingNeedsAsOn = errors.New("reporting: a batch ageing report needs an as-on date to be relative to")

// ExpiryBucket names one ageing band. The values are stable identifiers, safe to
// key a UI or a saved filter on; the human label is on the rollup.
type ExpiryBucket string

const (
	// ExpiryExpired is stock whose expiry date has PASSED. It is still on hand
	// and still counted; the flag is the whole point.
	ExpiryExpired ExpiryBucket = "EXPIRED"
	// ExpiryDueIn30 expires today or within the next 30 days, inclusive.
	ExpiryDueIn30 ExpiryBucket = "DUE_IN_30_DAYS"
	// ExpiryDueIn90 expires after 30 days and within 90, inclusive.
	ExpiryDueIn90 ExpiryBucket = "DUE_IN_90_DAYS"
	// ExpiryBeyond90 expires more than 90 days out.
	ExpiryBeyond90 ExpiryBucket = "BEYOND_90_DAYS"
	// ExpiryNotTracked is a position whose batch carries no expiry date, and
	// every position that carries no batch at all. It is not an absence of
	// information about perishable stock — most stock simply does not perish.
	ExpiryNotTracked ExpiryBucket = "NO_EXPIRY"
)

// expiryBands is the fixed print order, nearest deadline first. Every report
// carries ALL of them, empty ones included: a band that vanished when it was
// empty would make "no expired stock" indistinguishable from "the expired band
// was dropped somewhere", and the first is worth stating out loud.
var expiryBands = []ExpiryBucket{
	ExpiryExpired, ExpiryDueIn30, ExpiryDueIn90, ExpiryBeyond90, ExpiryNotTracked,
}

var expiryBandLabels = map[ExpiryBucket]string{
	ExpiryExpired:    "Expired",
	ExpiryDueIn30:    "Expiring within 30 days",
	ExpiryDueIn90:    "Expiring within 90 days",
	ExpiryBeyond90:   "Expiring beyond 90 days",
	ExpiryNotTracked: "No expiry date",
}

// bucketFor classifies a batch by days from the as-on date to its expiry.
//
// It is a pure function of two facts and nothing else — no clock, no database —
// so the band boundaries can be stated as a table in the test. Negative days
// mean the date has passed.
//
// The `<=` on each bound is inclusive DELIBERATELY: see the file header. Turning
// one into `<` silently promotes a batch expiring exactly on the boundary into
// the safer-looking band beyond it.
func bucketFor(hasExpiry bool, daysToExpiry int) ExpiryBucket {
	switch {
	case !hasExpiry:
		return ExpiryNotTracked
	case daysToExpiry < 0:
		return ExpiryExpired
	case daysToExpiry <= 30:
		return ExpiryDueIn30
	case daysToExpiry <= 90:
		return ExpiryDueIn90
	default:
		return ExpiryBeyond90
	}
}

// daysBetween counts CALENDAR days from `from` to `to`.
//
// Both instants are reduced to their civil date in UTC before subtracting. A
// plain to.Sub(from) would be at the mercy of whatever location the driver
// attached to a DATE column and of a daylight-saving jump inside the interval,
// either of which can turn 30 days into 29.958 and truncate to 29 — an
// off-by-one on a band boundary, arriving only twice a year, in one hemisphere.
func daysBetween(from, to time.Time) int {
	a := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.UTC)
	b := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, time.UTC)
	return int(b.Sub(a).Hours() / 24)
}

// --------------------------------------------------------------------------
// Report shapes
// --------------------------------------------------------------------------

// BatchStockRow is one (item, godown, batch) position with its batch dates and
// the band it falls in.
//
// Positions that carry NO batch appear here too, in ExpiryNotTracked. They are
// part of what the company owns, so leaving them out would stop the bands
// footing to the Stock Summary's total — and the point of this report is to be
// reconcilable with that one.
type BatchStockRow struct {
	StockItemID uuid.UUID
	ItemName    string
	UnitSymbol  string
	GodownID    uuid.UUID
	GodownName  string
	BatchID     uuid.UUID
	BatchName   string

	// MfgDate/ExpiryDate are ISO dates, "" when the batch does not carry one
	// (and always "" for a position with no batch).
	MfgDate    string
	ExpiryDate string
	// DaysToExpiry is signed: negative once the date has passed. It is
	// meaningful only when ExpiryDate is non-empty and is zero otherwise.
	DaysToExpiry int
	// Expired is the FLAG this report exists to raise. The row is still counted
	// in every total; see the file header.
	Expired bool
	Bucket  ExpiryBucket

	// Position is the replayed quartet, promoted, exactly as on
	// StockPositionRow — including a ClosingRate, which IS meaningful here
	// because a batch row is a single position and not a fold.
	inventory.Position
}

// BatchItemRollup is one item's batches folded together: the item total, plus
// how much of it has expired.
//
// Quantities are safe at this level and only at this level — one item, one unit.
type BatchItemRollup struct {
	StockItemID uuid.UUID
	ItemName    string
	UnitSymbol  string

	StockFigures

	// ExpiredQty/ExpiredValue are the part of the closing position sitting in
	// expired batches. They are a SUBSET of ClosingQty/ClosingValue, never a
	// deduction from them.
	ExpiredQty   decimal.Decimal
	ExpiredValue decimal.Decimal

	Rows []BatchStockRow
}

// ExpiryBandRollup is one ageing band across every item: what it is worth, and
// which rows are in it. Value only — see the file header on units.
type ExpiryBandRollup struct {
	Bucket ExpiryBucket
	Label  string

	ClosingValue decimal.Decimal
	// Rows is how many positions fell in this band, which is the only count
	// that means anything across items.
	RowCount int

	Rows []BatchStockRow
}

// BatchStockSummary is the batch-wise position as at one date, banded by expiry.
//
// It is a POSITION and takes ONE date. TotalValue must equal
// StockSummary.TotalValue for the same date, and the bands must sum to it — an
// expired batch is inside that identity, not outside it.
type BatchStockSummary struct {
	// AsOn is the boundary the replay was ACTUALLY bounded at — always
	// RESOLVED, never the caller's parameter (see resolvedStockAsOn). Unlike
	// its two siblings this one can never be empty: an ageing band is relative
	// to a date, so a missing one is ErrAgeingNeedsAsOn.
	AsOn string

	Rows  []BatchStockRow
	Items []BatchItemRollup
	// Bands are ALL five, in nearest-deadline-first order, empty ones included.
	Bands []ExpiryBandRollup

	TotalValue decimal.Decimal
	// ExpiredValue is the headline: the money sitting in stock that can no
	// longer be sold. It is part of TotalValue, not netted off it.
	ExpiredValue decimal.Decimal
}

// --------------------------------------------------------------------------
// Batch master dates
// --------------------------------------------------------------------------

// batchDates is a batch's manufacturing and expiry dates. Absent dates are
// normal: batches exist to separate physical lots, and only some industries date
// them.
type batchDates struct {
	MfgDate    *time.Time
	ExpiryDate *time.Time
}

// loadBatchDates reads every visible batch's dates.
//
// NO PREDICATE ON expiry_date APPEARS HERE, and none may. Filtering expired
// batches out of the master read would leave their stock in the report with no
// date and no band — it would be printed as "no expiry", which is a quieter
// version of the same lie the file header is about. Whether a batch is expired
// is decided ONE place: bucketFor.
func loadBatchDates(ctx context.Context, tx pgx.Tx) (map[uuid.UUID]batchDates, error) {
	rows, err := tx.Query(ctx, `SELECT id, mfg_date, expiry_date FROM stock_batch`)
	if err != nil {
		return nil, fmt.Errorf("reporting: query batch dates: %w", err)
	}
	defer rows.Close()

	out := map[uuid.UUID]batchDates{}
	for rows.Next() {
		var (
			id uuid.UUID
			d  batchDates
		)
		if err := rows.Scan(&id, &d.MfgDate, &d.ExpiryDate); err != nil {
			return nil, fmt.Errorf("reporting: scan batch dates: %w", err)
		}
		out[id] = d
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reporting: iterate batch dates: %w", err)
	}
	return out, nil
}

// --------------------------------------------------------------------------
// The report
// --------------------------------------------------------------------------

// BatchStockSummary returns the batch-wise stock position as at `asOn`, with
// every position classified into exactly one expiry band.
//
// `asOn` is REQUIRED (ErrAgeingNeedsAsOn) — an ageing band is relative to a date
// by definition.
func (s *Service) BatchStockSummary(ctx context.Context, companyID uuid.UUID, asOn string) (BatchStockSummary, error) {
	if err := scope(ctx, companyID); err != nil {
		return BatchStockSummary{}, err
	}
	// Resolve BEFORE the two checks below, so the date this report ages
	// against, the date it bounds the replay at, and the date it echoes are one
	// value (see resolvedStockAsOn). This report was already the SAFE one of the
	// three — time.Parse is strict, so a raw parameter that reached the echo had
	// to be a well-formed ISO date and raw therefore equalled resolved — but it
	// held by an argument about a parser rather than by construction, and a
	// whitespace-only date came back as a parse error rather than as the
	// "this report needs a date" answer that is actually true of it.
	asOn = resolvedStockAsOn(asOn)
	if asOn == "" {
		return BatchStockSummary{}, ErrAgeingNeedsAsOn
	}
	asOnDate, err := time.Parse(isoDate, asOn)
	if err != nil {
		return BatchStockSummary{}, fmt.Errorf("reporting: as-on date %q is not an ISO date: %w", asOn, err)
	}

	out := BatchStockSummary{
		AsOn:         asOn,
		TotalValue:   decimal.Zero,
		ExpiredValue: decimal.Zero,
	}

	err = s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		positions, _, err := stockPositionsAsOn(ctx, tx, asOn)
		if err != nil {
			return err
		}
		dates, err := loadBatchDates(ctx, tx)
		if err != nil {
			return err
		}

		for _, p := range positions {
			out.Rows = append(out.Rows, batchRowOf(p, dates[p.BatchID], asOnDate))
		}
		sortBatchRows(out.Rows)

		// EVERY row is folded into its item and its band. There is no filter in
		// this loop — an expired row contributes to the item's closing quantity
		// exactly like any other, and is additionally counted in the expired
		// subtotals. See the file header.
		out.Items = rollupBatchItems(out.Rows)
		out.Bands = bandBatchRows(out.Rows)
		for _, r := range out.Rows {
			out.TotalValue = out.TotalValue.Add(r.ClosingValue)
			if r.Expired {
				out.ExpiredValue = out.ExpiredValue.Add(r.ClosingValue)
			}
		}
		return nil
	})
	if err != nil {
		return BatchStockSummary{}, err
	}
	return out, nil
}

// batchRowOf decorates a replayed position with its batch dates and band.
func batchRowOf(p StockPositionRow, d batchDates, asOn time.Time) BatchStockRow {
	row := BatchStockRow{
		StockItemID: p.StockItemID,
		ItemName:    p.ItemName,
		UnitSymbol:  p.UnitSymbol,
		GodownID:    p.GodownID,
		GodownName:  p.GodownName,
		BatchID:     p.BatchID,
		BatchName:   p.BatchName,
		Position:    p.Position,
	}
	if d.MfgDate != nil {
		row.MfgDate = d.MfgDate.Format(isoDate)
	}
	hasExpiry := d.ExpiryDate != nil
	if hasExpiry {
		row.ExpiryDate = d.ExpiryDate.Format(isoDate)
		row.DaysToExpiry = daysBetween(asOn, *d.ExpiryDate)
	}
	row.Bucket = bucketFor(hasExpiry, row.DaysToExpiry)
	row.Expired = row.Bucket == ExpiryExpired
	return row
}

// sortBatchRows prints the report in the order a stock controller reads it:
// soonest deadline first, then by item, location and batch.
//
// Expired stock therefore sits at the TOP of the report, which is the only
// place it can do its job. The trailing id compares keep the order total, so a
// company with two nameless dimensions cannot get two different printouts of
// the same books.
func sortBatchRows(rows []BatchStockRow) {
	bandOrder := map[ExpiryBucket]int{}
	for i, b := range expiryBands {
		bandOrder[b] = i
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Bucket != b.Bucket {
			return bandOrder[a.Bucket] < bandOrder[b.Bucket]
		}
		if a.ExpiryDate != b.ExpiryDate {
			return a.ExpiryDate < b.ExpiryDate
		}
		if a.ItemName != b.ItemName {
			return a.ItemName < b.ItemName
		}
		if a.GodownName != b.GodownName {
			return a.GodownName < b.GodownName
		}
		if a.BatchName != b.BatchName {
			return a.BatchName < b.BatchName
		}
		if a.GodownID != b.GodownID {
			return a.GodownID.String() < b.GodownID.String()
		}
		return a.BatchID.String() < b.BatchID.String()
	})
}

// rollupBatchItems folds the batch rows into per-item totals, carrying the
// expired part as a SUBSET rather than a deduction.
func rollupBatchItems(rows []BatchStockRow) []BatchItemRollup {
	var out []BatchItemRollup
	idx := map[uuid.UUID]int{}
	for _, r := range rows {
		i, ok := idx[r.StockItemID]
		if !ok {
			i = len(out)
			idx[r.StockItemID] = i
			out = append(out, BatchItemRollup{
				StockItemID:  r.StockItemID,
				ItemName:     r.ItemName,
				UnitSymbol:   r.UnitSymbol,
				StockFigures: zeroFigures(),
				ExpiredQty:   decimal.Zero,
				ExpiredValue: decimal.Zero,
			})
		}
		g := &out[i]
		g.add(r.Position)
		if r.Expired {
			g.ExpiredQty = g.ExpiredQty.Add(r.ClosingQty)
			g.ExpiredValue = g.ExpiredValue.Add(r.ClosingValue)
		}
		g.Rows = append(g.Rows, r)
	}
	return out
}

// bandBatchRows files every row into EXACTLY ONE band, and returns all five
// bands whether or not they hold anything.
//
// Exactly one is guaranteed by construction: the band is a field on the row,
// bucketFor's switch is total, and this loop appends once. That matters because
// the alternative — five predicates evaluated independently — is how a row ends
// up counted in two bands (bounds that overlap) or in none (bounds with a gap),
// and either makes the bands stop summing to the total.
func bandBatchRows(rows []BatchStockRow) []ExpiryBandRollup {
	idx := map[ExpiryBucket]int{}
	out := make([]ExpiryBandRollup, 0, len(expiryBands))
	for _, b := range expiryBands {
		idx[b] = len(out)
		out = append(out, ExpiryBandRollup{
			Bucket:       b,
			Label:        expiryBandLabels[b],
			ClosingValue: decimal.Zero,
		})
	}
	for _, r := range rows {
		at, ok := idx[r.Bucket]
		if !ok {
			// Unreachable: bucketFor's switch is total over expiryBands. The
			// guard exists because the zero value of ExpiryBucket is "", and a
			// bare map lookup would return index 0 for it — quietly filing
			// perfectly good stock under EXPIRED, the one band this report is
			// read for.
			at = idx[ExpiryNotTracked]
		}
		band := &out[at]
		band.ClosingValue = band.ClosingValue.Add(r.ClosingValue)
		band.RowCount++
		band.Rows = append(band.Rows, r)
	}
	return out
}
