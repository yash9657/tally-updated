package reporting

// orderbook_overdue_test.go — WHAT A DELIVERY PROMISE IS LATE AGAINST, with no
// database.
//
// ⚠️ closing_stock_internal_test.go's header says it is "the only file in
// internal/reporting that lives in `package reporting`". That was true when it
// was written; this is the second, and it is noted here rather than corrected
// there because the two files are owned by different lanes.
//
// ⛔⛔ THE DEFECT THESE TESTS PIN.
//
// OrderBookRow.Overdue short-circuited to false whenever its `asOn` argument was
// blank — and blank is THE DEFAULT, because F2 sets no report period until an
// operator asks for one. So the Sales and Purchase Order Books, in the state an
// operator actually opens them, reported ZERO late deliveries however many there
// were, and reported it in the most convincing way available: no red rows, and
// the "N line(s) are past their agreed due date" note simply absent.
//
// The confusion was between two different empties. An absent DUE DATE means "no
// delivery date was ever agreed", which is genuinely never overdue and is still
// treated that way below. An absent AS-AT date means "the walk is unbounded" —
// and an unbounded walk runs up to the present, so the present is what a promised
// date is late against.
//
// The tests are pure on purpose: the decision is now a pure function of (what was
// asked for, what time it is), which is the only shape that can be asserted
// without waiting for tomorrow.

import (
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func obDec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// ⛔ THE REFERENCE DATE, RESOLVED ONCE PER REPORT.
func TestResolveOverdueAsOnFallsBackToTodayAndOnlyToToday(t *testing.T) {
	now := time.Date(2026, 8, 8, 3, 15, 0, 0, time.UTC)

	if got := resolveOverdueAsOn("", now); got != "2026-08-08" {
		t.Errorf("with NO as-at date the reference must be today (%s), got %q — an empty bound does "+
			"not mean there is no today, it means the walk is unbounded", "2026-08-08", got)
	}
	// An explicit date WINS, and is not quietly replaced by today. A report run
	// "as at 31 July" must judge lateness as at 31 July, or it would report an
	// August-due line as late inside a July position.
	if got := resolveOverdueAsOn("2026-07-31", now); got != "2026-07-31" {
		t.Errorf("an explicit as-at date must be the reference, got %q", got)
	}
	// Whitespace is not a date. Trimmed rather than passed through, because a
	// blank-looking value that reached time.Parse would fail and silently answer
	// "not overdue" — which is the defect wearing a different hat.
	if got := resolveOverdueAsOn("   ", now); got != "2026-08-08" {
		t.Errorf("a whitespace-only as-at date must fall back to today, got %q", got)
	}
	// UTC, and not the machine's local midnight: two servers in two zones must not
	// disagree about whether a line is late.
	local := time.Date(2026, 8, 9, 1, 0, 0, 0, time.FixedZone("IST", 5*3600+1800))
	if got := resolveOverdueAsOn("", local); got != "2026-08-08" {
		t.Errorf("the reference must be resolved in UTC, got %q for %s", got, local)
	}
}

// ⛔⛔ THE DEFECT ITSELF: THE DEFAULT VIEW MUST FLAG A LATE LINE.
func TestTheDefaultOrderBookStateStillFlagsAnOverdueLine(t *testing.T) {
	// A line promised for 15 July, on a report nobody gave a date to.
	row := OrderBookRow{
		OrderNo: "SO-2026-001", StockItem: "MS Angle",
		Qty: obDec("50"), Fulfilled: obDec("30"), Rate: obDec("300"),
		DueDate:     "2026-07-15",
		OverdueAsOn: "2026-08-08",
	}

	// THE CALL internal/server ACTUALLY MAKES. handler_orderbook.go passes
	// `book.AsOn`, which is "" in the default state — so this is the exact
	// argument the wire sees, not a convenient stand-in.
	if !row.Overdue("") {
		t.Error("a line due 2026-07-15, read on a book with no as-at date, is NOT flagged overdue. " +
			"That is the state an operator opens the report in, so the Sales and Purchase Order " +
			"Books report zero late deliveries on books full of them")
	}

	// AND THE CONTROLS, so the flag is not simply stuck on.
	notYet := row
	notYet.DueDate = "2026-09-30"
	if notYet.Overdue("") {
		t.Error("a line due 2026-09-30 must NOT be overdue today — the flag is answering true for " +
			"every dated line, which is a different and equally useless report")
	}
	undated := row
	undated.DueDate = ""
	if undated.Overdue("") {
		t.Error("⛔ a line with NO agreed due date must NEVER be overdue, however old. Treating an " +
			"absent date as today's invents a commitment the business never made")
	}
	// A row built by hand, outside OrderBookFor, has no reference and cannot
	// decide. False rather than a guess: this path is a programming error, not the
	// ordinary case it used to be.
	orphan := row
	orphan.OverdueAsOn = ""
	if orphan.Overdue("") {
		t.Error("a row with neither an argument nor a resolved reference must answer false")
	}
}

// ⚠️ AN EXPLICIT AS-AT DATE STILL WINS, so a historical position is judged
// historically. This is the half a plain "use today instead" fix would have
// broken, silently, on every report an operator DID set a period for.
func TestAnExplicitAsAtDateIsWhatLatenessIsJudgedAgainst(t *testing.T) {
	row := OrderBookRow{
		DueDate:     "2026-07-15",
		OverdueAsOn: "2026-07-31",
	}
	if !row.Overdue("2026-07-31") {
		t.Error("due 15 July, read as at 31 July: overdue")
	}
	if row.Overdue("2026-07-01") {
		t.Error("due 15 July, read as at 1 July: NOT overdue — a position as at a past date must " +
			"not be told about a promise that had not yet come due")
	}
	// Due exactly ON the as-at date is not yet late: the day is not over.
	if row.Overdue("2026-07-15") {
		t.Error("due 15 July, read as at 15 July: NOT overdue — the promise is for that day")
	}
}

// ⛔ THE NOTE ON THE ARTEFACT IS PART OF THE FIX, NOT DECORATION.
//
// The overdue count is what an operator reads when the table is long, and it was
// computed against b.AsOn — empty in the default state — so it never appeared
// there either. Two claims are asserted: the count is now made, and the sentence
// NAMES the date it was made against, because with no as-at date the rows are
// unbounded while lateness is measured against today, and an unstated mixture of
// two bounds is a count nobody can reproduce.
func TestTheNotesCountOverdueLinesAndSayWhatTheyCountedAgainst(t *testing.T) {
	book := OrderBook{
		Direction:   SalesOrders,
		AsOn:        "", // THE DEFAULT STATE — no F2 period was set.
		OverdueAsOn: "2026-08-08",
		Rows: []OrderBookRow{
			{DueDate: "2026-07-15", OverdueAsOn: "2026-08-08"}, // late
			{DueDate: "2026-09-30", OverdueAsOn: "2026-08-08"}, // not yet due
			{DueDate: "", OverdueAsOn: "2026-08-08"},           // no date agreed — never late
		},
	}
	notes := strings.Join(orderBookNotes(book), "\n")

	if !strings.Contains(notes, "1 outstanding line(s) are past their agreed due date") {
		t.Errorf("the default-state notes do not count the ONE late line:\n%s", notes)
	}
	if !strings.Contains(notes, "2026-08-08") {
		t.Errorf("the notes must NAME the date lateness was measured against, or the count cannot "+
			"be reproduced from the artefact:\n%s", notes)
	}
	if !strings.Contains(notes, "1 of 3 outstanding line(s) carry NO agreed due date") {
		t.Errorf("the undated line must still be excused rather than counted late:\n%s", notes)
	}

	// AND THE CONTROL: an explicit as-at date is echoed, and lateness is stated to
	// be measured against the same day rather than against today.
	dated := book
	dated.AsOn = "2026-07-31"
	dated.OverdueAsOn = "2026-07-31"
	for i := range dated.Rows {
		dated.Rows[i].OverdueAsOn = "2026-07-31"
	}
	datedNotes := strings.Join(orderBookNotes(dated), "\n")
	if !strings.Contains(datedNotes, "As at 2026-07-31") {
		t.Errorf("an explicit as-at date must be named on the artefact:\n%s", datedNotes)
	}
	if strings.Contains(datedNotes, "measured against TODAY") {
		t.Errorf("a report run for an explicit date must not claim lateness was measured against "+
			"today:\n%s", datedNotes)
	}
	if !strings.Contains(datedNotes, "1 outstanding line(s) are past their agreed due date") {
		t.Errorf("due 15 July is still late as at 31 July:\n%s", datedNotes)
	}
}
