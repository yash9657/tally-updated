package printing

// report.go — ONE generic tabular renderer for every statement an accountant is
// handed: Trial Balance, Balance Sheet, Profit & Loss, Day Book, Outstandings,
// Stock Summary, Payroll Register and Payslip.
//
// WHY ONE RENDERER AND NOT EIGHT. Those eight reports differ in their columns,
// their title and their tree depth — not in how they go on paper. Every one of
// them is a company block, a title, a period, a ruled column header, a body of
// indented rows, a ruled total block and a page number. Eight renderers would be
// eight places for the header to stop repeating on page 12 and eight places for a
// figure to get clipped. So internal/server maps each report's data into
// ReportTable and this file is the only thing that knows about paper.
//
// THIS FILE IMPORTS NOTHING FROM internal/reporting OR internal/payroll, and it
// must not: the direction of the dependency is that the caller assembles a
// ReportTable, exactly as doc.go describes, and internal/archtest enforces it.
// The consequence is that no number is computed here. Row.Cells arrive as
// ALREADY-FORMATTED strings (FormatMoney at the seam), so this renderer cannot
// disagree with the ledger about an amount — it can only place it or refuse to.

import (
	"errors"
	"fmt"
	"strings"
)

// ---------------------------------------------------------------------------
// The errors this renderer raises rather than printing something wrong
// ---------------------------------------------------------------------------

// ErrReportHasNoColumns says a ReportTable arrived with no columns. There is no
// honest fallback: a table with no columns has no geometry, and inventing one
// column to hold "everything" would print a report whose figures are in no
// column at all.
var ErrReportHasNoColumns = errors.New("printing: a report with no columns cannot be printed")

// ErrReportHasNoIssuer says a statement arrived with NO COMPANY NAME on it.
//
// WHY AN ANONYMOUS STATEMENT IS REFUSED RATHER THAN PRINTED WITH A BLANK
// LETTERHEAD. Everything else on a Balance Sheet is a figure ABOUT something;
// the company name is the only line that says what the figures are about. A
// Trial Balance with an empty letterhead is not a slightly untidy document — it
// is a page of amounts attributable to nobody, and these are the documents a
// bank reads to decide whose books it is holding, an auditor attaches to a
// report, and an accountant signs. Two companies' statements printed on the same
// afternoon become indistinguishable the moment they are off the screen, and
// nothing on the paper can tell them apart again.
//
// This FOLLOWS voucherdoc.go, which already refuses any document whose
// Supplier.Name is blank ("a document with no issuer is not evidence of
// anything"). The two renderers used to disagree — a receipt for Rs 200 was
// refused for having no issuer while a signed Balance Sheet was not — and there
// is no reading of that difference in which the statement is the safer of the
// two. A whitespace-only name is treated as absent for the same reason invBlank
// treats one as absent in rule46.go: it prints as nothing, so accepting it puts
// the blank on the page anyway.
//
// It is a DATA condition and not a layout fault, which is why *ReportIssuerError
// also unwraps to ErrVoucherParticularMissing: the company master is missing a
// particular, an operator fixes it in the master, and internal/server's
// writePrintError already answers that sentinel with 422 and the message
// verbatim rather than with a 500 nobody can act on.
var ErrReportHasNoIssuer = errors.New("printing: a statement with no company name on it is unattributable and will not be printed")

// ReportIssuerError names the statement that could not be attributed, and states
// the consequence rather than the field — the same shape, and for the same
// reason, as voucherdoc.go's VoucherParticularError.
type ReportIssuerError struct {
	Title string // the statement's own title, e.g. "Balance Sheet"
}

func (e *ReportIssuerError) Error() string {
	title := strings.TrimSpace(e.Title)
	if title == "" {
		// A table with neither a company nor a title. Naming it "this statement"
		// is honest; printing an empty %q would read as a bug in the message.
		title = "this statement"
	}
	return fmt.Sprintf("printing: %s cannot be printed because the company it belongs to has no name: a statement is evidence about ONE set of books, and one with a blank letterhead attributes every figure on it to nobody — a bank or an officer holding it cannot say whose statement it is, and two companies' statements become indistinguishable once they are off the screen. The name is the one line on the sheet that is not a figure, so it is the one line that cannot be left out",
		title)
}

// Unwrap returns BOTH sentinels: the specific one, so a caller or a test can say
// exactly which refusal this was, and the shared missing-particular one, so the
// HTTP layer answers it as the 422 data condition it is without having to learn
// a new error per renderer.
func (e *ReportIssuerError) Unwrap() []error {
	return []error{ErrReportHasNoIssuer, ErrVoucherParticularMissing}
}

// ErrRowWiderThanTable says a Row carries more cells than the table has columns.
//
// IT IS AN ERROR AND NOT A SILENT DROP. Padding a short row with blanks is safe —
// a Trial Balance group heading legitimately has no figures — but discarding a
// cell that has nowhere to go would mean a mapping bug in internal/server prints
// as a report that is merely missing a column of amounts, which is precisely the
// kind of fault nobody notices until a period is closed on it.
var ErrRowWiderThanTable = errors.New("printing: a report row has more cells than the table has columns")

// ErrFigureColumnTooNarrow says a figure did not fit its column.
//
// WHY THIS IS AN ERROR WHERE A LABEL WOULD ONLY WRAP. A right-aligned column is
// this package's marker for a FIGURE column (every amount in this codebase is
// set with TextRight — see page.go), and a figure is the one thing on a
// statement that may never be abbreviated. A Trial Balance whose 1,23,456.78
// printed as "1,23,45" or as "1,23,4..." is a WRONG NUMBER on a document an
// accountant signs and a bank reads, and it is wrong in a way that looks
// deliberate. Wrapping is no better: an amount split over two lines reads as two
// amounts. So the document refuses to be produced, the caller is told which
// figure and which column, and a human widens the column.
var ErrFigureColumnTooNarrow = errors.New("printing: a figure does not fit its column and a figure is never abbreviated")

// ErrLabelColumnTooNarrow says a column has not enough width to print even an
// abbreviated label in it.
//
// WHY NOT PRINT A BLANK. A column too narrow for "..." can hold nothing at all,
// and a blank Particulars cell on a Trial Balance is an amount attributed to no
// account — which reads as a real, if mysterious, line rather than as a layout
// fault. The weights are the caller's to fix, so the caller is told.
var ErrLabelColumnTooNarrow = errors.New("printing: a column is too narrow to print even an abbreviated label")

// reportEmptyNotice is what an empty report says on its single page. An empty
// statement is a real answer — "no entries in this period" — and it must arrive
// as a page that says so, because a zero-byte download reads as a broken system
// and sends somebody looking for a fault that is not there.
const reportEmptyNotice = "There are no entries to report."

// reportEllipsis marks a LABEL that was too long to print in full. Three ASCII
// dots, not U+2026: WinAnsi has a code for the ellipsis glyph, but the point of
// this mark is that a reviewer greps the uncompressed stream for it, and "..."
// is what they will type.
const reportEllipsis = "..."

// ---------------------------------------------------------------------------
// Geometry. All of it in POINTS, all of it float64 — this is paper, not money
// (see the note in fmtNum). No amount is computed anywhere in this file.
// ---------------------------------------------------------------------------

const (
	reportMargin = 36.0 // half an inch on every side

	reportCompanySize    = 13.0 // the company name, HelveticaBold
	reportCompanyLead    = 15.0 // baseline-to-baseline when the company name wraps
	reportLetterheadSize = 8.0  // Overrides.HeaderLines
	reportLetterheadLead = 10.0
	reportTitleSize      = 12.0
	reportSubtitleSize   = 9.0
	reportColHeaderSize  = 8.0
	reportColHeaderLead  = 9.5
	reportBodySize       = 8.5
	reportBodyLead       = 11.5
	reportPageNumSize    = 8.0

	reportCellPad    = 3.0  // clear space inside a column, each side
	reportIndentStep = 11.0 // one level of Row.Indent
	reportRuleGap    = 3.0  // clear space a RuleAbove/RuleBelow claims
	reportRuleWidth  = 0.5
	reportBlockGap   = 7.0 // between the letterhead, the title and the table

	// reportMaxCellLines caps how tall one wrapped LABEL cell may grow. A row
	// that could grow without limit is a row that can be taller than the page,
	// and the pagination would then have nowhere to put it. Four lines is enough
	// for the longest real ledger name; beyond that the label is truncated with a
	// visible reportEllipsis, which is a mark a reader can see and question — as
	// against a silent clip, which is indistinguishable from the name simply
	// being shorter than it is.
	reportMaxCellLines = 4

	// reportMinLabelWidth is the width a LABEL column must keep for itself, in
	// points: enough for reportEllipsis at reportBodySize (three Helvetica full
	// stops measure 7.09pt), rounded up. Row.Indent is clamped so that it can
	// never eat past this — a Balance Sheet five levels deep must still print the
	// ledger name, because the indent is decoration and the name is the data.
	reportMinLabelWidth = 8.0
)

// ---------------------------------------------------------------------------
// RenderReport
// ---------------------------------------------------------------------------

// RenderReport lays out t and returns the finished PDF bytes.
//
// HOW "Page N of M" IS POSSIBLE. M is not known while the rows are being placed,
// so this runs in two phases over one layout: reportPaginate does the whole
// layout WITHOUT DRAWING and returns the pages with their rows already assigned;
// only then, with len(pages) in hand, does the drawing phase stamp each page's
// furniture. Nothing in pagination depends on the page number, which is what
// makes the two phases agree — see reportGeom.bodyFloor for the one place that
// could have broken the property, and why it does not.
//
// It returns an error rather than a document in five cases. FOUR ARE CALLER OR
// CONFIGURATION BUGS: no columns, a row wider than the table, a figure that does
// not fit its column, and a label column too narrow to print even an
// abbreviation. THE FIFTH IS A DATA CONDITION — a company with no name, which an
// operator fixes in the master (see ErrReportHasNoIssuer). Every one of them
// would otherwise print as a plausible-looking statement with something missing
// from it, and the missing thing would not be noticed until the statement was
// somewhere it could not be recalled from.
func RenderReport(t ReportTable) ([]byte, error) {
	// WHOSE BOOKS THIS IS, FIRST. Before any geometry: a statement that cannot be
	// attributed should not be laid out at all, and checking it here rather than
	// in newReportGeom keeps the refusal in the one function every caller goes
	// through.
	if strings.TrimSpace(t.Company.Name) == "" {
		return nil, &ReportIssuerError{Title: t.Title}
	}
	if len(t.Columns) == 0 {
		return nil, fmt.Errorf("%w: %q", ErrReportHasNoColumns, t.Title)
	}

	size := A4Portrait
	if t.Landscape {
		size = A4Landscape
	}

	g := newReportGeom(t, size)

	body, err := g.layoutRows(t.Rows, false)
	if err != nil {
		return nil, err
	}
	footer, err := g.layoutRows(t.Footer, true)
	if err != nil {
		return nil, err
	}

	pages := g.paginate(body)

	doc := NewDoc(size)
	for i, rows := range pages {
		p := doc.NewPage()
		g.stampFurniture(p, i+1, len(pages))
		y := g.firstRowTop
		for _, r := range rows {
			y = g.drawRow(p, r, y)
		}
		if len(rows) == 0 {
			// The empty report: say so where the first row would have been.
			p.TextCenter((g.tableLeft+g.tableRight)/2, g.firstRowTop-reportBodySize,
				HelveticaOblique, reportBodySize, reportEmptyNotice)
			y -= reportBodyLead
		}
		if i == len(pages)-1 {
			g.drawFooterBlock(p, footer, y)
		}
	}

	return doc.Bytes()
}

// ---------------------------------------------------------------------------
// reportGeom: the page's fixed furniture, measured once
// ---------------------------------------------------------------------------

// reportGeom is everything about the page that is the same on every sheet: the
// column x positions, the wrapped column headers, where the body may start and
// where it must stop.
type reportGeom struct {
	t    ReportTable
	size PageSize

	tableLeft  float64
	tableRight float64

	// colX has len(Columns)+1 entries: colX[i] is column i's left edge and
	// colX[i+1] its right edge, so the last entry is the table's right edge.
	colX []float64

	// colHeaderLines[i] is column i's header, wrapped to its own width.
	colHeaderLines [][]string
	colHeaderRows  int

	// THE LETTERHEAD IS MEASURED HERE AND DRAWN FROM THESE SLICES, so the
	// height reserved for it and the lines actually put on the paper cannot
	// disagree — the same pattern colHeaderLines already uses.
	//
	// companyLines is Company.Name wrapped to the table width; headerLines is
	// Overrides.HeaderLines, each entry wrapped and the results flattened, so
	// len(headerLines) is the number of BASELINES and not the number of
	// configured lines. footerLines is Overrides.FooterLines the same way, which
	// is what makes measureFooterBlock's reserve match drawFooterBlock's draw.
	companyLines []string
	headerLines  []string
	footerLines  []string

	firstRowTop float64 // top edge of the first body row's box
	footerFloor float64 // the lowest y the footer block may reach

	footerBlockH float64 // height reserved for Footer rows + Overrides.FooterLines
}

// newReportGeom measures the page furniture and computes the column geometry.
//
// COLUMN WIDTHS ARE WEIGHTS, NOT POINTS (doc.go, Column.Width). The usable width
// is divided in those proportions, which is exactly why the same ReportTable can
// be handed to a portrait or a landscape page and neither caller restates the
// geometry. A non-positive weight is treated as 1: a zero-width column would
// otherwise be an invisible column whose cells overlap its neighbour, and a
// caller who left Width unset meant "an ordinary column", not "no column".
func newReportGeom(t ReportTable, size PageSize) *reportGeom {
	g := &reportGeom{
		t:          t,
		size:       size,
		tableLeft:  reportMargin,
		tableRight: size.Width - reportMargin,
	}

	usable := g.tableRight - g.tableLeft
	total := 0.0
	weights := make([]float64, len(t.Columns))
	for i, c := range t.Columns {
		w := c.Width
		if w <= 0 {
			w = 1
		}
		weights[i] = w
		total += w
	}
	g.colX = make([]float64, len(t.Columns)+1)
	g.colX[0] = g.tableLeft
	acc := 0.0
	for i, w := range weights {
		acc += w
		// Positions are accumulated from the RUNNING TOTAL rather than added up
		// column by column, so the last column's right edge lands exactly on
		// tableRight instead of a few hundredths short of it. A rule that stops
		// short of the column it is meant to close is visible on paper.
		g.colX[i+1] = g.tableLeft + usable*acc/total
	}

	// THE LETTERHEAD WRAPS. Company.Name used to be handed straight to
	// TextCenter, which centres a string on the sheet whether or not it fits:
	// "Shri Mahalaxmi Engineering & Fabrication Works Private Limited (Bhiwandi and
	// Wada Units)" came off the printer with its first AND last words over the edge of
	// the paper, on every page of every Trial Balance, Balance Sheet, P&L and Day
	// Book — the one line that says whose books these are, clipped at both ends on
	// a statement an accountant signs and a bank reads. Shrinking the font was the
	// other option and is not a fix: a name long enough to need it once will be
	// long enough again.
	//
	// The wrap width is the TABLE width, because the name is centred on the table.
	//
	// vdFit AND NOT Wrap (it is defined in voucherdoc.go; both files are one
	// package): Wrap normalises whitespace, and Overrides.HeaderLines is text a
	// company typed into its own template — a line laid out with deliberate gaps
	// ("Phone: ...   E-Mail: ...") must not have them closed up merely because it
	// passed through the renderer. A line that already fits is drawn as given; only
	// one that does not fit is re-flowed, because the alternative there is a line
	// clipped off both edges of the sheet. vdFit always returns at least one line
	// (an empty one for an empty name), so len(companyLines)-1 is a safe
	// extra-leading count.
	g.companyLines = vdFit(HelveticaBold, reportCompanySize, g.t.Company.Name, usable)
	for _, line := range t.Overrides.HeaderLines {
		g.headerLines = append(g.headerLines, vdFit(Helvetica, reportLetterheadSize, line, usable)...)
	}
	for _, line := range t.Overrides.FooterLines {
		g.footerLines = append(g.footerLines, vdFit(Helvetica, reportLetterheadSize, line, usable)...)
	}

	// The column headers wrap like any other label: a header is text, so an
	// over-long one is broken across lines rather than refused. That is the one
	// place a right-aligned column behaves like a label and not like a figure,
	// because "Closing Balance (Cr)" is a phrase and 1,23,456.78 is not.
	g.colHeaderLines = make([][]string, len(t.Columns))
	g.colHeaderRows = 1
	for i, c := range t.Columns {
		lines := Wrap(HelveticaBold, reportColHeaderSize, c.Header, g.cellWidth(i, 0))
		g.colHeaderLines[i] = lines
		if len(lines) > g.colHeaderRows {
			g.colHeaderRows = len(lines)
		}
	}

	g.firstRowTop = g.headerBottom()
	g.footerBlockH = g.measureFooterBlock()
	g.footerFloor = reportMargin + reportPageNumSize + reportBlockGap

	return g
}

// cellWidth is the text width available inside column i once indentPts points of
// indent have been taken off the left: the column, less the padding on each side,
// less the indent.
func (g *reportGeom) cellWidth(i int, indentPts float64) float64 {
	return g.colX[i+1] - g.colX[i] - 2*reportCellPad - indentPts
}

// clampIndent turns a Row.Indent LEVEL into points, never taking so much of the
// column that no label can be printed. See reportMinLabelWidth.
func (g *reportGeom) clampIndent(i, level int) float64 {
	if level <= 0 {
		return 0
	}
	want := float64(level) * reportIndentStep
	room := g.colX[i+1] - g.colX[i] - 2*reportCellPad - reportMinLabelWidth
	if want > room {
		want = room
	}
	if want < 0 {
		want = 0
	}
	return want
}

// letterheadBottom is the y below the company block: the company name (which may
// have wrapped to more than one line) plus Overrides.HeaderLines. The letterhead
// is a company's own text and is repeated on EVERY page, because page 7 of a Day
// Book handed to a bank has to say whose books it is.
//
// IT COUNTS THE LINES THE DRAWING CODE WILL DRAW, not the fields the caller set:
// both this and stampFurniture walk g.companyLines and g.headerLines, so a name
// that wrapped to three lines pushes the title down by exactly the leading those
// three lines occupy. Counting Overrides.HeaderLines instead — as this did before
// the letterhead wrapped — would put the title through the last letterhead line
// the moment either of them needed a second row.
func (g *reportGeom) letterheadBottom() float64 {
	y := g.size.Height - reportMargin - reportCompanySize
	y -= float64(len(g.companyLines)-1) * reportCompanyLead
	y -= float64(len(g.headerLines)) * reportLetterheadLead
	return y
}

// titleBottom is the y below the title and subtitle block.
func (g *reportGeom) titleBottom() float64 {
	y := g.letterheadBottom() - reportBlockGap - reportTitleSize
	if g.t.Subtitle != "" {
		y -= reportSubtitleSize + 2
	}
	return y
}

// headerTopRule is the y of the rule that opens the column header band.
func (g *reportGeom) headerTopRule() float64 {
	return g.titleBottom() - reportBlockGap
}

// headerBottom is the y of the rule that closes the column header band, which is
// also the top edge of the first body row.
func (g *reportGeom) headerBottom() float64 {
	y := g.headerTopRule() - reportRuleGap
	y -= reportColHeaderSize
	y -= float64(g.colHeaderRows-1) * reportColHeaderLead
	return y - reportRuleGap
}

// measureFooterBlock is the height the total block and Overrides.FooterLines
// will need.
func (g *reportGeom) measureFooterBlock() float64 {
	h := 0.0
	if len(g.t.Footer) > 0 {
		// A rule closes the body off from the totals.
		h += reportRuleGap
		for _, r := range g.t.Footer {
			h += g.rowHeight(r)
		}
	}
	// g.footerLines and not Overrides.FooterLines: a closing note long enough to
	// wrap needs a baseline per WRAPPED line, and reserving one per configured
	// line would run the note off the foot of the sheet past the page number.
	h += float64(len(g.footerLines)) * reportLetterheadLead
	if len(g.footerLines) > 0 {
		h += reportBlockGap
	}
	return h
}

// bodyFloor is the lowest y a BODY row's box may reach.
//
// THE ONE PLACE THAT COULD HAVE BROKEN "Page N of M". The total block prints on
// the LAST page only, so the honest floor is lower on every earlier page — but a
// floor that depended on which page was last would make pagination depend on the
// page count, which is derived from pagination. So the footer block's height is
// reserved on EVERY page: at worst a few rows move to the next sheet, and in
// exchange the page count is known before a single glyph is drawn and the last
// page is guaranteed to have room for its totals. Reserving nothing and
// discovering on the last page that the totals do not fit is the alternative,
// and it prints a Trial Balance with no total.
func (g *reportGeom) bodyFloor() float64 {
	return g.footerFloor + g.footerBlockH
}

// ---------------------------------------------------------------------------
// Row layout
// ---------------------------------------------------------------------------

// reportRow is one Row with its cells already wrapped to their columns and its
// height already known, so that pagination is arithmetic and the drawing phase
// makes no layout decision a second time.
type reportRow struct {
	src       Row
	lines     [][]string // lines[i] is column i's wrapped text
	rows      int        // the tallest cell's line count
	h         float64    // the box height, rules included
	indentPts float64    // Row.Indent in points, already clamped
}

// layoutRows wraps, measures and validates every row. isFooter only changes the
// error messages, so that a caller told a figure does not fit knows whether to
// look at the body or at the total line.
func (g *reportGeom) layoutRows(rows []Row, isFooter bool) ([]reportRow, error) {
	out := make([]reportRow, 0, len(rows))
	for n, r := range rows {
		if len(r.Cells) > len(g.t.Columns) {
			where := "row"
			if isFooter {
				where = "footer row"
			}
			return nil, fmt.Errorf("%w: %s %d of %q carries %d cells for %d columns, so %q would be dropped",
				ErrRowWiderThanTable, where, n+1, g.t.Title, len(r.Cells), len(g.t.Columns),
				strings.Join(r.Cells[len(g.t.Columns):], " | "))
		}
		lr := reportRow{
			src:       r,
			lines:     make([][]string, len(g.t.Columns)),
			rows:      1,
			indentPts: g.clampIndent(0, r.Indent),
		}
		for i := range g.t.Columns {
			cell := ""
			if i < len(r.Cells) {
				cell = r.Cells[i]
			}
			indentPts := 0.0
			if i == 0 {
				indentPts = lr.indentPts
			}
			avail := g.cellWidth(i, indentPts)
			f := reportRowFont(r)

			if cell == "" {
				lr.lines[i] = []string{""}
				continue
			}
			if g.t.Columns[i].Align != AlignRight && avail < reportMinLabelWidth {
				return nil, fmt.Errorf("%w: column %q of %q offers %s points and %s are needed for %q alone, so %q would print as a blank cell",
					ErrLabelColumnTooNarrow, g.t.Columns[i].Header, g.t.Title,
					fmtNum(avail), fmtNum(reportMinLabelWidth), reportEllipsis, cell)
			}

			if g.t.Columns[i].Align == AlignRight {
				// A figure column: never wrapped, never truncated, never
				// abbreviated. If it does not fit, no document is produced.
				if Measure(f, reportBodySize, cell) > avail {
					where := "row"
					if isFooter {
						where = "footer row"
					}
					return nil, fmt.Errorf("%w: %q in column %q of %s %d of %q needs %s points and the column offers %s",
						ErrFigureColumnTooNarrow, cell, g.t.Columns[i].Header, where, n+1, g.t.Title,
						fmtNum(Measure(f, reportBodySize, cell)), fmtNum(avail))
				}
				lr.lines[i] = []string{cell}
				continue
			}

			// A LABEL WITH NOWHERE TO WRAP IS ABBREVIATED, NOT SPLIT. Wrap()
			// hard-breaks a single over-wide token at the character that overflows,
			// which is right for a 200-character description but wrong for the two
			// things that actually turn up in a narrow column: a date and a voucher
			// number. "01-Apr-202" on one line and "6" on the next reads as corrupt
			// data — a reader cannot tell whether the date is wrong or the print is
			// — whereas "01-Apr..." says plainly that the column is too narrow for
			// what is in it. So a cell with no break opportunity is truncated with a
			// visible ellipsis on ONE line.
			var lines []string
			if !strings.ContainsAny(cell, " \t\n") && Measure(f, reportBodySize, cell) > avail {
				lr.lines[i] = []string{reportTruncate(f, reportBodySize, cell, avail)}
				continue
			}
			lines = Wrap(f, reportBodySize, cell, avail)
			if len(lines) > reportMaxCellLines {
				kept := append([]string{}, lines[:reportMaxCellLines]...)
				kept[reportMaxCellLines-1] = reportTruncate(f, reportBodySize, kept[reportMaxCellLines-1], avail)
				lines = kept
			}
			lr.lines[i] = lines
			if len(lines) > lr.rows {
				lr.rows = len(lines)
			}
		}
		lr.h = g.rowHeightFor(r, lr.rows)
		out = append(out, lr)
	}
	return out, nil
}

// reportRowFont is Helvetica, or HelveticaBold for a total or a group heading
// (Row.Bold).
func reportRowFont(r Row) Font {
	if r.Bold {
		return HelveticaBold
	}
	return Helvetica
}

// rowHeight measures a row without laying its cells out, for the footer reserve.
// It deliberately assumes ONE line per cell and is only ever used on Footer rows,
// whose cells are a label and a figure; measureFooterBlock is a RESERVE, and if a
// footer label ever wrapped the reserve would be short, which is why
// drawFooterBlock draws the totals immediately under the last body row rather
// than pinned to the reserved space.
func (g *reportGeom) rowHeight(r Row) float64 { return g.rowHeightFor(r, 1) }

func (g *reportGeom) rowHeightFor(r Row, lines int) float64 {
	h := float64(lines) * reportBodyLead
	if r.RuleAbove {
		h += reportRuleGap
	}
	if r.RuleBelow {
		h += reportRuleGap
	}
	return h
}

// reportTruncate cuts s down until it fits maxWidth WITH the ellipsis appended,
// so the mark is inside the column and not over the next one. It always returns
// at least the ellipsis: a cell that shows only "..." is visibly abbreviated,
// which is the point.
func reportTruncate(f Font, size float64, s string, maxWidth float64) string {
	if Measure(f, size, s+reportEllipsis) <= maxWidth {
		return s + reportEllipsis
	}
	for i := len(s); i > 0; i-- {
		if !isReportRuneStart(s, i) {
			continue
		}
		cand := strings.TrimRight(s[:i], " ") + reportEllipsis
		if Measure(f, size, cand) <= maxWidth {
			return cand
		}
	}
	return reportEllipsis
}

// isReportRuneStart reports whether i is a rune boundary in s, so truncation
// never cuts a multi-byte rune in half and hands the encoder a fragment.
func isReportRuneStart(s string, i int) bool {
	if i <= 0 || i >= len(s) {
		return true
	}
	return s[i]&0xC0 != 0x80
}

// ---------------------------------------------------------------------------
// Pagination
// ---------------------------------------------------------------------------

// paginate assigns rows to pages. It always returns at least one page, so an
// empty report is a page that says so rather than ErrNoPages.
func (g *reportGeom) paginate(rows []reportRow) [][]reportRow {
	floor := g.bodyFloor()
	pages := [][]reportRow{{}}
	y := g.firstRowTop
	for _, r := range rows {
		if y-r.h < floor && len(pages[len(pages)-1]) > 0 {
			pages = append(pages, []reportRow{})
			y = g.firstRowTop
		}
		// A single row taller than the whole body area is placed anyway, on a page
		// of its own. It will run into the footer band, which is ugly and visible;
		// the alternative is a loop that never places it, which is a print request
		// that never returns. reportMaxCellLines makes it unreachable from a cell,
		// so this is the belt to that braces.
		cur := len(pages) - 1
		pages[cur] = append(pages[cur], r)
		y -= r.h
	}
	return pages
}

// ---------------------------------------------------------------------------
// Drawing
// ---------------------------------------------------------------------------

// stampFurniture draws everything that repeats: the company block, the
// letterhead, the title, the subtitle, the ruled column headers and the
// "Page N of M" footer.
//
// EVERY PAGE GETS ALL OF IT. A 40-page Day Book whose page 12 has no column
// headers is unusable — the reader cannot tell the debit column from the credit
// column, which on a Day Book is the difference between money in and money out.
func (g *reportGeom) stampFurniture(p *Page, page, total int) {
	center := (g.tableLeft + g.tableRight) / 2

	// The letterhead, from the lines newReportGeom measured. letterheadBottom
	// counts these same two slices, which is what keeps the title clear of them.
	y := g.size.Height - reportMargin - reportCompanySize
	for i, line := range g.companyLines {
		if i > 0 {
			y -= reportCompanyLead
		}
		p.TextCenter(center, y, HelveticaBold, reportCompanySize, line)
	}
	for _, line := range g.headerLines {
		y -= reportLetterheadLead
		p.TextCenter(center, y, Helvetica, reportLetterheadSize, line)
	}

	title := g.t.Title
	if g.t.Overrides.Title != "" {
		title = g.t.Overrides.Title
	}
	y = g.letterheadBottom() - reportBlockGap - reportTitleSize
	p.TextCenter(center, y, HelveticaBold, reportTitleSize, title)
	if g.t.Subtitle != "" {
		y -= reportSubtitleSize + 2
		p.TextCenter(center, y, Helvetica, reportSubtitleSize, g.t.Subtitle)
	}

	top := g.headerTopRule()
	p.Line(g.tableLeft, top, g.tableRight, top, reportRuleWidth)
	base := top - reportRuleGap - reportColHeaderSize
	for i, c := range g.t.Columns {
		x0 := g.colX[i] + reportCellPad
		x1 := g.colX[i+1] - reportCellPad
		for k, line := range g.colHeaderLines[i] {
			reportDrawCell(p, x0, x1, base-float64(k)*reportColHeaderLead,
				c.Align, HelveticaBold, reportColHeaderSize, line)
		}
	}
	bottom := g.headerBottom()
	p.Line(g.tableLeft, bottom, g.tableRight, bottom, reportRuleWidth)

	// "Page N of M" — N of M and not a bare N, because a reader who is handed a
	// stapled statement has to be able to tell that a sheet is missing.
	p.TextRight(g.tableRight, reportMargin, Helvetica, reportPageNumSize,
		fmt.Sprintf("Page %d of %d", page, total))
}

// drawRow draws one laid-out row starting at box top y and returns the box top of
// the next row.
func (g *reportGeom) drawRow(p *Page, r reportRow, y float64) float64 {
	top := y
	if r.src.RuleAbove {
		p.Line(g.tableLeft, top, g.tableRight, top, reportRuleWidth)
		top -= reportRuleGap
	}
	f := reportRowFont(r.src)
	base := top - reportBodySize
	for i, c := range g.t.Columns {
		x0 := g.colX[i] + reportCellPad
		x1 := g.colX[i+1] - reportCellPad
		if i == 0 && r.indentPts > 0 {
			// Row.Indent renders the group tree: a Balance Sheet's "Fixed Assets"
			// sits at the margin and the ledgers under it step in. The indent moves
			// the LEFT edge only, so a right-aligned first column (rare, but legal)
			// stays where its figures line up.
			x0 += r.indentPts
		}
		for k, line := range r.lines[i] {
			reportDrawCell(p, x0, x1, base-float64(k)*reportBodyLead, c.Align, f, reportBodySize, line)
		}
	}
	bottom := base - float64(r.rows-1)*reportBodyLead - (reportBodyLead - reportBodySize)
	if r.src.RuleBelow {
		p.Line(g.tableLeft, bottom, g.tableRight, bottom, reportRuleWidth)
		bottom -= reportRuleGap
	}
	return bottom
}

// drawFooterBlock draws the total rows on the LAST page, ruled off from the body,
// immediately under the last body row — which is where a total belongs and where
// Tally puts it. Overrides.FooterLines follow, then the page number, which
// stampFurniture has already drawn.
//
// y IS THE BOTTOM OF THE LAST BODY ROW, passed in rather than recomputed. The
// drawing phase already walked the rows and knows exactly where they stopped;
// re-deriving it here would be a second implementation of the same arithmetic,
// and the two would eventually disagree by a leading — which on paper is a total
// rule struck through the last entry.
func (g *reportGeom) drawFooterBlock(p *Page, footer []reportRow, y float64) {
	if len(footer) > 0 {
		p.Line(g.tableLeft, y, g.tableRight, y, reportRuleWidth)
		y -= reportRuleGap
		for _, r := range footer {
			y = g.drawRow(p, r, y)
		}
	}
	if len(g.footerLines) > 0 {
		y -= reportBlockGap
		center := (g.tableLeft + g.tableRight) / 2
		for _, line := range g.footerLines {
			p.TextCenter(center, y, Helvetica, reportLetterheadSize, line)
			y -= reportLetterheadLead
		}
	}
}

// reportDrawCell places one line of text inside the content box [x0,x1] at
// baseline y, honouring the column's alignment. Right alignment is what makes a
// column of amounts comparable at a glance and is how every figure column on
// every one of these statements is set.
func reportDrawCell(p *Page, x0, x1, y float64, align Align, f Font, size float64, s string) {
	if s == "" {
		return
	}
	switch align {
	case AlignRight:
		p.TextRight(x1, y, f, size, s)
	case AlignCenter:
		p.TextCenter((x0+x1)/2, y, f, size, s)
	default:
		p.Text(x0, y, f, size, s)
	}
}
