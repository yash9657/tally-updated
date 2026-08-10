package reporting_test

// stock_as_on_resolution_test.go — THE DATE A STOCK REPORT ECHOES MUST BE THE
// DATE IT ACTUALLY BOUNDED AT.
//
// WHAT THIS FILE IS ABOUT, AND WHY IT IS NOT A COSMETIC TEST
// ---------------------------------------------------------
// Three reports publish an `AsOn` field that a client renders as a caption over
// the figures: StockSummary, GodownStockSummary and BatchStockSummary. All three
// echoed the CALLER'S PARAMETER. A caption is a claim about the FIGURES, so it
// has to come from the same value the figures did, or it is an assumption
// printed in the same font as a fact — the shape that made the Bank
// Reconciliation header render "As at " with nothing after it.
//
// ⛔ AND UNDERNEATH THE CAPTION THERE IS A VALUE DEFECT, WHICH IS WHY THE
// ASSERTIONS BELOW READ MONEY AND NOT DATES ALONE. The as-on date reaches TWO
// consumers that do not agree about whitespace:
//
//   - the movement query, via nullDate() -> Postgres, which TRIMS;
//   - the openings guard, which compares it as a GO STRING against
//     company.books_beginning_from.
//
// So " 2026-06-30" bounds the movements at 2026-06-30 while making the guard
// read `" 2026-06-30" < "2025-04-01"` — TRUE, because a space sorts below a
// digit — and the WHOLE OPENING POSITION disappears. The report still foots,
// still renders, still sorts, and is short by exactly the stock the company
// began with. On the fixture below that is 10 units and 1,000.00 of value, and
// a check that read only the closing QUANTITY of the movements would not see
// it either — this repository has been burned three times by exactly that
// oracle.
//
// ⚠️ HOW REACHABLE, STATED HONESTLY. No HTTP caller can send an untrimmed date
// today: `reportDate` in package server trims and then rejects anything
// time.Parse will not take. The ECHO half is driven — the Stock Summary screen
// renders `as_on` and prints a sentence from it. The whitespace half is defence
// in depth for in-process callers (closing_stock.go's Balance Sheet fold-in
// goes through the same derivation) and, more importantly, it is what makes the
// echo EQUAL to the bound by construction rather than by two maintainers
// keeping two copies of a date in step.

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/reporting"
)

// asOnBooks plants one item whose whole quartet is exact, so every figure below
// is arithmetic a reader can redo:
//
//	opening   10 @ 100  -> qty 10, value 1000, running average 100
//	IN        10 @ 300  -> qty 20, value 4000, running average 200
//	OUT        5        -> consumed at 200 = 1000; qty 15, value 3000
//
// Drop the opening (the defect) and the same replay gives qty 5 and value 1500:
// a third of the units and half the money, on a report that still adds up.
type asOnBooks struct {
	*stockBooks
	item   uuid.UUID
	batch  uuid.UUID
	godown uuid.UUID
}

func asOnSetup(t *testing.T) *asOnBooks {
	t.Helper()
	b := stockSetup(t) // the company's books begin 2025-04-01
	a := &asOnBooks{stockBooks: b, godown: b.main}
	a.item = b.newItem(t, "Zinc Ingot")
	a.batch = b.newBatch(t, a.item, "LOT-A")
	b.opening(t, a.item, a.godown, a.batch, "10", "100")
	b.post(t, "2026-05-10",
		stockLine(a.item, a.godown, a.batch, ledger.Inward, "10", "300", "0"))
	b.post(t, "2026-06-01",
		stockLine(a.item, a.godown, a.batch, ledger.Outward, "5", "999", "0"))
	return a
}

// expectFullPosition asserts the WHOLE quartet — every quantity AND every value
// — plus the names the row is filed under. A closing-quantity-only check passes
// against a report that has lost the opening's money, and a total-only check
// passes against a report that has lost a row and gained another.
func expectFullPosition(t *testing.T, label string, r reporting.StockPositionRow) {
	t.Helper()
	if r.ItemName != "Zinc Ingot" {
		t.Errorf("%s: the position is filed under item %q, want %q", label, r.ItemName, "Zinc Ingot")
	}
	if r.GodownName != "Main Location" {
		t.Errorf("%s: the position is filed under location %q, want %q", label, r.GodownName, "Main Location")
	}
	if r.BatchName != "LOT-A" {
		t.Errorf("%s: the position is filed under batch %q, want %q", label, r.BatchName, "LOT-A")
	}
	eq(t, label+": opening quantity", r.OpeningQty, dec("10"))
	eq(t, label+": opening VALUE", r.OpeningValue, dec("1000"))
	eq(t, label+": inward quantity", r.InwardQty, dec("10"))
	eq(t, label+": inward VALUE", r.InwardValue, dec("3000"))
	eq(t, label+": outward quantity", r.OutwardQty, dec("5"))
	eq(t, label+": outward VALUE (cost consumed, not the 999 it sold for)",
		r.OutwardValue, dec("1000"))
	eq(t, label+": closing quantity", r.ClosingQty, dec("15"))
	eq(t, label+": closing VALUE", r.ClosingValue, dec("3000"))
}

// --------------------------------------------------------------------------

// TestStockSummaryEchoesTheBoundaryItAppliedAndNotTheCallersString is the
// headline: the same instant, spelled two ways by a caller, must produce ONE
// answer and ONE caption.
func TestStockSummaryEchoesTheBoundaryItAppliedAndNotTheCallersString(t *testing.T) {
	b := asOnSetup(t)

	clean := b.summary(t, "2026-06-30")
	expectFullPosition(t, "as at 2026-06-30", positionRow(t, clean, b.item, b.godown, b.batch))
	eq(t, "the whole inventory as at 2026-06-30", clean.TotalValue, dec("3000"))
	if clean.AsOn != "2026-06-30" {
		t.Errorf("a report asked for 2026-06-30 echoes %q", clean.AsOn)
	}

	// ⛔ THE SAME DATE, SPELLED WITH THE WHITESPACE A NON-HTTP CALLER CAN CARRY.
	// The movement query trims it and the openings guard did not, so this used
	// to be a DIFFERENT REPORT: 5 units and 1,500.00 under a caption reading
	// " 2026-06-30 ".
	padded := b.summary(t, " 2026-06-30 ")
	if padded.AsOn != "2026-06-30" {
		t.Errorf("the report echoes the caller's string %q instead of the boundary %q it resolved — "+
			"a caption is a claim about the FIGURES and must come from the value the figures did",
			padded.AsOn, "2026-06-30")
	}
	expectFullPosition(t, "as at ' 2026-06-30 '", positionRow(t, padded, b.item, b.godown, b.batch))
	eq(t, "the whole inventory as at ' 2026-06-30 '", padded.TotalValue, dec("3000"))

	// And the two reports are the same report, row for row.
	if len(padded.Rows) != len(clean.Rows) {
		t.Fatalf("the padded date produced %d positions and the clean one %d — %s vs %s",
			len(padded.Rows), len(clean.Rows), describePositions(padded), describePositions(clean))
	}
	for i := range clean.Rows {
		if !padded.Rows[i].ClosingValue.Equal(clean.Rows[i].ClosingValue) ||
			!padded.Rows[i].ClosingQty.Equal(clean.Rows[i].ClosingQty) ||
			padded.Rows[i].ItemName != clean.Rows[i].ItemName {
			t.Errorf("position %d differs between the two spellings: %s %s/%s vs %s %s/%s",
				i,
				padded.Rows[i].ItemName, padded.Rows[i].ClosingQty.StringFixed(4),
				padded.Rows[i].ClosingValue.StringFixed(4),
				clean.Rows[i].ItemName, clean.Rows[i].ClosingQty.StringFixed(4),
				clean.Rows[i].ClosingValue.StringFixed(4))
		}
	}
}

// TestAnUnboundedStockSummaryEchoesNoDateRatherThanToday pins the OTHER half of
// the resolution, and it is the half that must NOT be "fixed" by defaulting.
//
// An empty as-on means the replay ran with NO upper bound: it includes vouchers
// dated after today. Echoing today's date would make the field a lie in the
// other direction — and BOUNDING at today would change which vouchers the
// replay sees, which MOVES THE BALANCE SHEET (the Closing Stock asset comes off
// this same derivation) and needs a deliberate verify:demo re-pin. So "" stays
// "", and every reader is required to say "no date limit" rather than "today".
func TestAnUnboundedStockSummaryEchoesNoDateRatherThanToday(t *testing.T) {
	b := asOnSetup(t)
	// A voucher dated well after any plausible "today" for this fixture. An
	// as-at-today report would exclude it; the unbounded one must include it,
	// which is what makes the caption question a real one.
	b.post(t, "2030-01-01",
		stockLine(b.item, b.godown, b.batch, ledger.Inward, "1", "500", "0"))

	all := b.summary(t, "")
	if all.AsOn != "" {
		t.Errorf("an UNBOUNDED stock summary echoes %q — the report applied no boundary at all, "+
			"and a date here would be read as one", all.AsOn)
	}
	// The post-dated receipt IS in it, in quantity AND in value: 15 + 1 units,
	// 3000 + 500.
	row := positionRow(t, all, b.item, b.godown, b.batch)
	eq(t, "the unbounded closing quantity", row.ClosingQty, dec("16"))
	eq(t, "the unbounded closing VALUE", row.ClosingValue, dec("3500"))
	eq(t, "the unbounded inventory total", all.TotalValue, dec("3500"))

	// The control: bounded at a date before that receipt, the same books give
	// the position the caption used to claim.
	asAt := b.summary(t, "2026-06-30")
	eq(t, "the bounded closing quantity", positionRow(t, asAt, b.item, b.godown, b.batch).ClosingQty, dec("15"))
	eq(t, "the bounded inventory total", asAt.TotalValue, dec("3000"))
}

// TestEveryStockReportResolvesTheSameDateTheSameWay is the CLASS assertion.
//
// Not "StockSummary trims" — the property: all three reports that publish an
// AsOn resolve it through one function, so no two of them can caption the same
// request differently, and none of them can caption figures it did not derive.
func TestEveryStockReportResolvesTheSameDateTheSameWay(t *testing.T) {
	b := asOnSetup(t)
	const want = "2026-06-30"

	for _, spelling := range []string{want, " " + want, want + " ", "  " + want + "  "} {
		summary := b.summary(t, spelling)
		godown := b.godownSummary(t, spelling)
		batch := b.batchSummary(t, spelling)

		for _, got := range []struct {
			report string
			asOn   string
		}{
			{"StockSummary", summary.AsOn},
			{"GodownStockSummary", godown.AsOn},
			{"BatchStockSummary", batch.AsOn},
		} {
			if got.asOn != want {
				t.Errorf("%s asked for %q echoes %q, want the resolved %q",
					got.report, spelling, got.asOn, want)
			}
		}

		// ⛔ AND THE FIGURES, NOT ONLY THE CAPTIONS. The three reports fold the
		// same positions three ways; if the date resolution ever differed
		// between them they would disagree about how much the company owns
		// while each one still footed. Value is the only figure additive across
		// items, which is why it is the one compared here — and it is compared
		// against a HAND-COMPUTED constant, not merely against each other, so
		// three reports agreeing on a wrong number cannot pass.
		eq(t, "StockSummary total for "+spelling, summary.TotalValue, dec("3000"))
		eq(t, "GodownStockSummary total for "+spelling, godown.TotalValue, dec("3000"))
		eq(t, "BatchStockSummary total for "+spelling, batch.TotalValue, dec("3000"))

		// The godown breakdown must still name the location it filed the stock
		// under — a fold that lost the opening would still total to something.
		if len(godown.Godowns) == 0 {
			t.Fatalf("the godown breakdown for %q has no columns at all", spelling)
		}
		found := false
		for _, g := range godown.Godowns {
			if g.GodownName == "Main Location" {
				found = true
				eq(t, "Main Location's closing value for "+spelling, g.ClosingValue, dec("3000"))
			}
		}
		if !found {
			t.Errorf("the godown breakdown for %q holds no 'Main Location' column", spelling)
		}
	}
}

// TestABlankBatchAgeingDateIsTheMissingDateAnswerAndNotAParseError pins the
// small honesty improvement the shared resolver buys the one report that
// REQUIRES a date. A whitespace-only parameter is a missing date, and the
// refusal that names the requirement is the one an operator can act on.
func TestABlankBatchAgeingDateIsTheMissingDateAnswerAndNotAParseError(t *testing.T) {
	b := asOnSetup(t)
	for _, blank := range []string{"", " ", "\t "} {
		_, err := b.rep.BatchStockSummary(b.ctx, b.company, blank)
		if !errors.Is(err, reporting.ErrAgeingNeedsAsOn) {
			t.Errorf("a batch ageing report asked for %q fails with %v, want ErrAgeingNeedsAsOn — "+
				"an ageing band is relative to a date, and that is what to tell the caller", blank, err)
		}
	}
	// The control: a real date still works, and a MALFORMED one is still
	// refused as malformed rather than quietly becoming "no date".
	if got := b.batchSummary(t, "2026-06-30").AsOn; got != "2026-06-30" {
		t.Errorf("the batch ageing report echoes %q", got)
	}
	if _, err := b.rep.BatchStockSummary(b.ctx, b.company, "30-06-2026"); err == nil {
		t.Error("a non-ISO as-on date was accepted by the batch ageing report")
	}
}
