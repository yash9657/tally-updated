package server

// handler_print_reports.go — GET /v1/companies/{companyID}/print/reports/{name}
//
// NINE reports print for real:
//
//	trial-balance   balance-sheet   profit-and-loss   day-book
//	outstandings    stock-summary   payroll-register  payroll-payslip
//	bank-reconciliation
//
// EVERY OTHER NAME IN authz.KnownReportNames STAYS MAPPED AND ANSWERS 501. The
// deferred set is, in full:
//
//	cash-flow, anomalies, gstr1, gstr3b, reconciliation-queue,
//	stock-movement, godown-summary, batch-summary, stock-item-vouchers,
//	itc-reconciliation, ims-inbox,
//	payroll-form-138, payroll-pf-ecr, payroll-esi-return,
//	tds-register,
//	cost-centre-summary, cost-category-summary,
//	sales-order-book, purchase-order-book,
//	budget-variance
//	gstr9
//
// The BUDGET VARIANCE (T11) is deferred because the screen's honesty does not
// survive being printed. It legitimately SUPPRESSES the grand total when a plan
// mixes a CLOSING_BALANCE line with a NETT_TRANSACTIONS one — a position plus a
// flow is not a quantity — and says so in a note beside the missing figure. Paper
// has no such affordance: a footer is either there or it is not, and a reader
// holding a printout cannot expand a caveat. The same applies to the UNBUDGETED
// section, which is deliberately outside the plan's totals on screen and which, as
// a second table under one heading, is exactly how it gets added to the first. So
// the printed form needs a layout that splits by basis or states the suppression
// in the footer, and that decision has not been taken.
//
// The GSTR-9 ANNUAL RECONCILIATION (T12) is deferred, and it is the sharpest case
// of the rule this whole header states. Every other deferred name is an internal
// statement whose printed form would merely be a different document. This one has
// the SHAPE OF A FILED STATUTORY RETURN, and it is explicitly not one: eleven of
// its fourteen tables are NOT_DERIVED, table 4's total is suppressed because five
// of its rows cannot be produced, and the whole thing carries ten unconditional
// disclosures beginning with `not-filing-ready`. On screen those live beside the
// figures and a reader cannot miss them. On paper, an annual return on our
// stationery with tidy tables and no room for ten caveats is a document somebody
// will hand to a tax officer. So the printed form needs a layout in which the
// disclosures are the FIRST thing on the page, and that decision has not been
// taken.
//
// The two order books (T3) are deferred because a printed order book is a
// different document from the screen. On paper, on our stationery, it reads as a
// statement of what the company has contracted to do — but its figures are
// outstanding quantities at the ORDER's agreed rate, which the eventual invoice
// may lawfully differ from. A printed version has to distinguish what is agreed
// from what is expected, and that is a layout decision nobody has taken. A blank
// or naive page that looks like a commitment schedule is worse than a refusal.
//
// The two cost reports (T2) are deferred for the reason bank-reconciliation
// originally was: the Cost Centre Summary is always for ONE cost category, and
// the print route is a flat literal carrying no axis. Printing "the first axis"
// would put a real, correctly rendered Department report on paper for a company
// whose question was about Branches, with nothing on the page saying so. The
// selector is a query parameter on the DISPLAY route; giving the print route the
// same treatment is a layout decision nobody has taken yet.
//
// They stay MAPPED on purpose. A name the router does not emit is a name an
// administrator's rule can never match (internal/authz/vocabulary.go's whole
// reason for existing), and for a `disallow` that is the silent-wrong-answer bug:
// the administrator believes they forbade printing the ECR and the system
// permits it. So each one is authorized normally and then answered 501 with a
// message naming the report — a stated gap, not a silent 404. Note that three of
// the deferred payroll names are FILE formats (the ECR, the ESI return and Form
// 138 are fixed-layout statutory uploads, already served as files by
// handler_payroll.go): "printing" them means a human-readable covering
// statement, which is a document nobody has specified yet, not a PDF of a
// pipe-delimited file.
//
// bank-reconciliation WAS deferred for a different and more interesting reason
// than the rest: not an unwritten layout but a design gap. The print route is
// /print/reports/{name} and carries no ledger id, while a Bank Reconciliation
// Statement is always FOR ONE BANK ACCOUNT, so there was no honest way to print
// "the" statement for a company with three banks.
//
// THE PRODUCT DECISION IS NOW TAKEN (HANDOFF-PARITY-SPRINT.md §3.4, option (c)):
//
//	?ledger_id=<uuid>   print that one account's statement   [the default shape]
//	?all=true           print every bank account, one section each, in one PDF
//	neither             400, naming both, because a company with three banks
//	                    that asked for "the" statement has not said which
//
// The two are mutually exclusive and passing both is a 400 rather than a silent
// precedence rule — an operator who passes both has one of two different
// documents in mind and the server cannot know which.
//
// WHY 400 AND NOT A DEFAULT. Picking "the first bank account" for a bare request
// is the failure the original refusal existed to prevent: it puts a real
// statement, correctly rendered, for the WRONG ACCOUNT on paper, and nothing on
// the page says so. The refusal's reasoning survives as the no-parameter case.
//
// The live JSON statement at GET /bank-reconciliation/{ledgerID} is unaffected.
//
// ===========================================================================
// TWO RULES THAT DECIDE EVERY LINE BELOW
// ===========================================================================
//  1. NO NUMBER IS RE-DERIVED FOR THE PRINTED COPY. Each builder calls the SAME
//     service method the JSON handler calls, and re-uses the SAME query-parameter
//     helpers (reportDate, outstandingKindParam, wageMonthParam). A printed Trial
//     Balance for a different date than the one on screen — or one that summed
//     its own total — is worse than no print button, because it is wrong and it
//     looks official.
//  2. FORMATTING HAPPENS AT THIS SEAM, from the decimal.Decimal the report
//     already returned: printing.FormatMoney / FormatQty / FormatDate. Never
//     fmt.Sprintf, never a float, and never inside the renderer (printing.Row
//     takes strings for exactly this reason — see internal/printing/doc.go).
//
// AND ONE CONTRACT FROM THE RENDERER: a FIGURE column must be
// printing.AlignRight. AlignRight is internal/printing's marker for "never
// abbreviate this", and a money column left as AlignLeft would be silently
// wrapped or ellipsised instead of raising ErrFigureColumnTooNarrow. Column.Width
// values are relative WEIGHTS, not points.

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/printing"
	"github.com/khata/cored/internal/reporting"
)

// handlePrintReport authorizes, builds a printing.ReportTable and renders it.
func (s *Server) handlePrintReport(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
	// The report name is a LITERAL path segment (routes() registers one route per
	// name from authz.KnownReportNames), read back with the same splitPath
	// routeAction authorized it with, so this handler cannot serve a different
	// report than the PDP made its decision about.
	segs := splitPath(routeSuffix(r))
	if len(segs) != 3 || segs[0] != "print" || segs[1] != "reports" {
		writeError(w, http.StatusNotFound, "not a report print route")
		return
	}
	name := segs[2]

	var (
		table printing.ReportTable
		built bool
	)
	switch name {
	case "trial-balance":
		table, built = s.printTrialBalance(ctx, w, r, companyID)
	case "balance-sheet":
		table, built = s.printBalanceSheet(ctx, w, r, companyID)
	case "profit-and-loss":
		table, built = s.printProfitAndLoss(ctx, w, r, companyID)
	case "day-book":
		table, built = s.printDayBook(ctx, w, r, companyID)
	case "outstandings":
		table, built = s.printOutstandings(ctx, w, r, companyID)
	// ⛔ THE PAYABLES AGEING PRINTS FROM THE SAME BUILDER (D94). Its route is
	// auto-mounted the moment the name enters `authz.KnownReportNames`, and a name with
	// no case here reaches the `default` arm — an honest 501, but a needless one when
	// the layout already exists and only the kind differs. `outstandingKindParam` is
	// shared between the JSON and printed forms on purpose, so both directions have
	// always printed from one parser; this passes the kind instead of parsing it.
	case "bills-payable":
		table, built = s.printOutstandingsOfKind(ctx, w, r, companyID, reporting.Payables)
	case "stock-summary":
		table, built = s.printStockSummary(ctx, w, r, companyID)
	case "payroll-register":
		table, built = s.printPayrollRegister(ctx, w, r)
	case "payroll-payslip":
		table, built = s.printPayrollPayslip(ctx, w, r)
	case "bank-reconciliation":
		table, built = s.printBankReconciliation(ctx, w, r, companyID)
	default:
		// Authorized, and honestly refused. See the file header for the full list
		// and for why these are 501 rather than absent routes.
		writeError(w, http.StatusNotImplemented,
			"printing the "+name+" report is not implemented yet: the ACL rule for "+
				"print report:"+name+" is honoured, but no printed layout has been "+
				"specified for it, so nothing is produced rather than something wrong")
		return
	}
	if !built {
		return // the builder already wrote a 4xx (a bad date, month or kind).
	}

	company, ok := s.printCompanyBlock(ctx, w, companyID)
	if !ok {
		return
	}
	table.Company = company

	// Per-company letterhead. RenderReport honours exactly three fields for a
	// statement — Title, HeaderLines (repeated on EVERY page) and FooterLines
	// (last page only). Declaration, Terms, SignatureLabel and the bank block are
	// deliberately IGNORED for KindReport: printing "Authorised Signatory" on a
	// Trial Balance would imply a certification nobody gave.
	overrides, err := s.printStore.LoadOverrides(ctx, companyID, printing.KindReport)
	if err != nil {
		writePrintError(w, err)
		return
	}
	table.Overrides = overrides

	pdf, err := printing.RenderReport(table)
	if err != nil {
		writePrintError(w, err)
		return
	}
	writePDF(w, pdfFilename(name, time.Now().UTC().Format("20060102")), pdf)
}

// ---------------------------------------------------------------------------
// The company letterhead
// ---------------------------------------------------------------------------

// printCompanyBlock reads the supplier/letterhead block for the printed page.
//
// !! DUPLICATE OF printingdb.loadSupplier, DELIBERATELY AND TEMPORARILY !!
// internal/printing/printingdb has this read already (loadSupplier), including
// migration 0015's rule that mailing_name is what PRINTS and an empty
// mailing_name means "use name", and the newline-block address split. It is
// unexported, and printingdb is another slice's file in this wave. The SQL below
// is therefore copied VERBATIM so a grep for the column list finds both, and the
// track handoff asks printingdb to export a Company(ctx, companyID) accessor.
// Until it does, this is the one place a printed REPORT's letterhead could come
// to disagree with a printed INVOICE's — the same company on two documents with
// two different addresses — so it must not be edited without editing the other.
func (s *Server) printCompanyBlock(ctx context.Context, w http.ResponseWriter, companyID uuid.UUID) (printing.Company, bool) {
	var (
		c                                                       printing.Company
		name, mailing, address, city, pincode, stateCode, gstin string
	)
	err := s.pool.RunInReadOnlyTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT name, mailing_name, address, city, pincode, state_code, gstin
			   FROM company WHERE company_id = $1 AND id = $1`,
			companyID).Scan(&name, &mailing, &address, &city, &pincode, &stateCode, &gstin)
	})
	if err != nil {
		// Under RLS an absent row is what a wrong or missing tenant GUC looks
		// like, so it is reported as the tenancy failure it is rather than as an
		// empty letterhead on a document that then goes out.
		writeError(w, http.StatusNotFound,
			"the company issuing this document is not visible in this transaction; "+
				"a statement cannot be printed on a blank letterhead")
		return printing.Company{}, false
	}
	c.Name = name
	if mailing != "" {
		c.Name = mailing
	}
	// The address BLOCK is newline-separated free text (migration 0015): line one
	// is Address1 and everything after it joins into Address2. Blank lines are
	// keystrokes, not content, and an empty printed line reads as a fault.
	var lines []string
	for _, l := range strings.Split(address, "\n") {
		if t := strings.TrimSpace(l); t != "" {
			lines = append(lines, t)
		}
	}
	if len(lines) > 0 {
		c.Address1 = lines[0]
		c.Address2 = strings.Join(lines[1:], ", ")
	}
	c.City, c.PIN, c.StateCode, c.GSTIN = city, pincode, stateCode, gstin
	return c, true
}

// ---------------------------------------------------------------------------
// Shared cell helpers
// ---------------------------------------------------------------------------

// printMoney is the ONE money formatter this file uses.
func printMoney(d decimal.Decimal) string { return printing.FormatMoney(d) }

// printMoneyOrBlank prints nothing for an exact zero.
//
// A Trial Balance line for a debit-balance ledger has NOTHING in the credit
// column; printing "0.00" there states that the ledger has a credit balance of
// zero, which is a different (and busier) claim than "this ledger is not on that
// side". Every figure that is genuinely zero and genuinely meaningful — a total,
// a net — goes through printMoney instead.
func printMoneyOrBlank(d decimal.Decimal) string {
	if d.IsZero() {
		return ""
	}
	return printing.FormatMoney(d)
}

// printISODate renders a stored ISO date (YYYY-MM-DD) the way every other date on
// a printed document is rendered. An unparseable value is passed through rather
// than blanked: a date the books hold in a shape this cannot read is something a
// reader must SEE, not something a printer should quietly drop.
func printISODate(iso string) string {
	iso = strings.TrimSpace(iso)
	if iso == "" {
		return ""
	}
	t, err := time.Parse("2006-01-02", iso)
	if err != nil {
		return iso
	}
	return printing.FormatDate(t)
}

// printAsOnSubtitle words the "as at" line of a POSITION statement.
func printAsOnSubtitle(asOn string) string {
	if strings.TrimSpace(asOn) == "" {
		return "As at the latest date in the books"
	}
	return "As at " + printISODate(asOn)
}

// printPeriodSubtitle words the period line of a PERIOD statement. Each of the
// four cases is spelled out because "For the period  to 31-Mar-2027" is the kind
// of half-worded heading that gets read as a typo rather than as an open bound.
func printPeriodSubtitle(from, to string) string {
	f, t := strings.TrimSpace(from), strings.TrimSpace(to)
	switch {
	case f == "" && t == "":
		return "For all dates in the books"
	case f == "":
		return "Up to " + printISODate(t)
	case t == "":
		return "From " + printISODate(f) + " onwards"
	default:
		return "From " + printISODate(f) + " to " + printISODate(t)
	}
}

// printLabelRow is a note row: one cell, the rest padded by the renderer
// (layoutRows pads a short row and errors only on a LONG one).
//
// ⚠️ IT IS NOT FULL WIDTH, WHATEVER ITS NAME ONCE SAID. There is no column
// spanning in layoutRows: a cell is confined to ITS OWN COLUMN, wrapped to at
// most four lines and then ellipsised. On a table whose first column is the
// 2-wide "Date", a sentence put here is present on the page and unreadable —
// which is how the Day Book's footer came to read "20 voucher version(s) in this
// period. No column total is...". Use printNoteRows for anything longer than a
// column heading; this stays for the SHORT labels that genuinely fit.
func printLabelRow(text string) printing.Row {
	return printing.Row{Cells: []string{text}}
}

// printNoteRows lays a NOTE into the widest label column of a table, as as many
// SINGLE-LINE rows as it takes.
//
// ⛔ WHY THE WRAPPING IS DONE HERE AND NOT LEFT TO THE RENDERER. layoutRows will
// wrap a cell, but only to reportMaxCellLines (four) and then it truncates with
// an ellipsis — and a multi-line FOOTER row also breaks the renderer's footer
// reserve, which measures footer rows assuming one line each. One line per row
// keeps both properties true, and it makes the line breaks something this file
// can be tested on.
//
// ⚠️ THE GEOMETRY BELOW MIRRORS internal/printing/report.go (A4 with a half-inch
// margin, columns divided in their relative weights, reportCellPad each side,
// Helvetica at reportBodySize). Mirroring is a duplicate that can drift, so it is
// deliberately CONSERVATIVE — it wraps to 92% of the true width — and it is
// backed by a test that extracts the text of a rendered PDF and fails if the
// note is not on the page in full, which is the only assertion that cannot be
// fooled by the mirror going stale.
func printNoteRows(cols []printing.Column, landscape bool, text string) []printing.Row {
	if len(cols) == 0 || strings.TrimSpace(text) == "" {
		return nil
	}
	// The WIDEST LEFT-ALIGNED column: a right-aligned one is a figure column, and
	// a sentence in it would be refused outright (ErrFigureColumnTooNarrow).
	idx, best := -1, 0.0
	total := 0.0
	for i, c := range cols {
		total += c.Width
		if c.Align != printing.AlignRight && c.Width > best {
			idx, best = i, c.Width
		}
	}
	if idx < 0 || total <= 0 {
		return []printing.Row{{Cells: []string{text}}}
	}

	page := printing.A4Portrait
	if landscape {
		page = printing.A4Landscape
	}
	const margin, cellPad, bodySize = 36.0, 3.0, 8.5
	avail := ((page.Width-2*margin)*best/total - 2*cellPad) * 0.92
	if avail <= 0 {
		return []printing.Row{{Cells: []string{text}}}
	}

	var out []printing.Row
	for _, line := range printing.Wrap(printing.Helvetica, bodySize, text, avail) {
		cells := make([]string, len(cols))
		cells[idx] = line
		out = append(out, printing.Row{Cells: cells})
	}
	return out
}

// ---------------------------------------------------------------------------
// 1. Trial Balance
// ---------------------------------------------------------------------------

func (s *Server) printTrialBalance(ctx context.Context, w http.ResponseWriter, r *http.Request, companyID uuid.UUID) (printing.ReportTable, bool) {
	// The SAME as_on parsing the JSON handler uses: an unparseable date is a 400,
	// never a silent fallback to "no bound".
	asOn, ok := reportDate(w, r, "as_on")
	if !ok {
		return printing.ReportTable{}, false
	}
	tb, err := s.report.TrialBalance(ctx, companyID, asOn)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return printing.ReportTable{}, false
	}

	// The FLAT per-ledger rows, which are exactly the `rows` the JSON handler
	// emits. The group-wise rollup is a UI affordance (a tree you expand); on
	// paper it would print every figure twice — once on the group line and once
	// on the ledger lines under it — and a reader adding up a column would get
	// double the total.
	rows := make([]printing.Row, 0, len(tb.Rows))
	for _, row := range tb.Rows {
		rows = append(rows, printing.Row{Cells: []string{
			row.LedgerName, row.GroupName,
			printMoneyOrBlank(row.Debit), printMoneyOrBlank(row.Credit),
		}})
	}

	footer := []printing.Row{{
		Cells:     []string{"Total", "", printMoney(tb.TotalDebit), printMoney(tb.TotalCredit)},
		Bold:      true,
		RuleAbove: true,
	}}
	// A Trial Balance that does not balance MUST SAY SO on the paper. Silence
	// here is the one presentation that lets a broken set of books be signed.
	if !tb.IsBalanced() {
		footer = append(footer, printing.Row{
			Cells: []string{"THIS TRIAL BALANCE DOES NOT BALANCE. Debits less credits",
				"", printMoney(tb.TotalDebit.Sub(tb.TotalCredit)), ""},
			Bold: true,
		})
	}

	return printing.ReportTable{
		Title:    "Trial Balance",
		Subtitle: printAsOnSubtitle(asOn),
		Columns: []printing.Column{
			{Header: "Particulars", Align: printing.AlignLeft, Width: 5},
			{Header: "Group", Align: printing.AlignLeft, Width: 3},
			{Header: "Debit", Align: printing.AlignRight, Width: 2},
			{Header: "Credit", Align: printing.AlignRight, Width: 2},
		},
		Rows:   rows,
		Footer: footer,
	}, true
}

// ---------------------------------------------------------------------------
// 2. Balance Sheet and 3. Profit & Loss — one shape, two statements
// ---------------------------------------------------------------------------

// printStatementColumns is the two-column geometry both two-sided statements use.
func printStatementColumns() []printing.Column {
	return []printing.Column{
		{Header: "Particulars", Align: printing.AlignLeft, Width: 6},
		{Header: "Amount", Align: printing.AlignRight, Width: 2},
	}
}

// printSectionRows renders one side of a two-sided statement: a ruled heading,
// the node tree indented by depth, and the side's total ruled off.
func printSectionRows(sec reporting.Section) []printing.Row {
	out := []printing.Row{{Cells: []string{sec.Title, ""}, Bold: true, RuleBelow: true}}
	out = append(out, printStatementNodeRows(sec.Nodes, 1)...)
	out = append(out, printing.Row{
		Cells:     []string{"Total " + sec.Title, printMoney(sec.Total)},
		Bold:      true,
		RuleAbove: true,
	})
	return out
}

// printStatementNodeRows walks the group/ledger tree. Indent carries the depth;
// the renderer clamps it so that a five-level-deep ledger still prints its NAME
// (the indent is decoration, the name is the data).
func printStatementNodeRows(nodes []reporting.StatementNode, level int) []printing.Row {
	var out []printing.Row
	for _, n := range nodes {
		out = append(out, printing.Row{
			Cells:  []string{n.Name, printMoney(n.Amount)},
			Indent: level,
			// A group or a computed line (Net Profit, Difference in Opening
			// Balances) is a total of what sits under it and is set bold; a ledger
			// leaf is a figure and is not.
			Bold: n.Kind != reporting.NodeLedger,
		})
		out = append(out, printStatementNodeRows(n.Children, level+1)...)
	}
	return out
}

func (s *Server) printBalanceSheet(ctx context.Context, w http.ResponseWriter, r *http.Request, companyID uuid.UUID) (printing.ReportTable, bool) {
	asOn, ok := reportDate(w, r, "as_on")
	if !ok {
		return printing.ReportTable{}, false
	}
	bs, err := s.report.BalanceSheet(ctx, companyID, asOn)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return printing.ReportTable{}, false
	}

	rows := printSectionRows(bs.Liabilities)
	rows = append(rows, printing.Row{Cells: []string{"", ""}})
	rows = append(rows, printSectionRows(bs.Assets)...)

	footer := []printing.Row{{
		Cells:     []string{"Net Profit carried to the Balance Sheet", printMoney(bs.NetProfit)},
		Bold:      true,
		RuleAbove: true,
	}}
	if !bs.IsBalanced() {
		footer = append(footer, printing.Row{
			Cells: []string{"THIS BALANCE SHEET DOES NOT BALANCE. Liabilities less assets",
				printMoney(bs.Liabilities.Total.Sub(bs.Assets.Total))},
			Bold: true,
		})
	}

	return printing.ReportTable{
		Title:    "Balance Sheet",
		Subtitle: printAsOnSubtitle(asOn),
		Columns:  printStatementColumns(),
		Rows:     rows,
		Footer:   footer,
	}, true
}

func (s *Server) printProfitAndLoss(ctx context.Context, w http.ResponseWriter, r *http.Request, companyID uuid.UUID) (printing.ReportTable, bool) {
	// A P&L is the one statement of the three for which a LOWER bound is
	// meaningful, so it takes both bounds — the same two params the JSON handler
	// takes, validated the same way.
	from, ok := reportDate(w, r, "from")
	if !ok {
		return printing.ReportTable{}, false
	}
	to, ok := reportDate(w, r, "to")
	if !ok {
		return printing.ReportTable{}, false
	}
	pl, err := s.report.ProfitAndLoss(ctx, companyID, from, to)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return printing.ReportTable{}, false
	}

	rows := printSectionRows(pl.Expenses)
	rows = append(rows, printing.Row{Cells: []string{"", ""}})
	rows = append(rows, printSectionRows(pl.Income)...)

	return printing.ReportTable{
		Title:    "Profit & Loss Account",
		Subtitle: printPeriodSubtitle(from, to),
		Columns:  printStatementColumns(),
		Rows:     rows,
		Footer: []printing.Row{{
			Cells:     []string{"Net Profit", printMoney(pl.NetProfit)},
			Bold:      true,
			RuleAbove: true,
		}},
	}, true
}

// ---------------------------------------------------------------------------
// 4. Day Book
// ---------------------------------------------------------------------------

func (s *Server) printDayBook(ctx context.Context, w http.ResponseWriter, r *http.Request, companyID uuid.UUID) (printing.ReportTable, bool) {
	// The JSON handler passes from/to through unvalidated. The printed copy
	// validates them, which is a difference worth stating: an unparseable date on
	// a screen shows an obviously wrong list, while on paper it produces a
	// confident statement of a period nobody asked for.
	from, ok := reportDate(w, r, "from")
	if !ok {
		return printing.ReportTable{}, false
	}
	to, ok := reportDate(w, r, "to")
	if !ok {
		return printing.ReportTable{}, false
	}
	dbk, err := s.report.DayBook(ctx, companyID, from, to)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return printing.ReportTable{}, false
	}

	// The columns are named here rather than inline in the table literal because
	// the footer note has to be laid out AGAINST them — see printNoteRows.
	cols := []printing.Column{
		{Header: "Date", Align: printing.AlignLeft, Width: 2},
		{Header: "Voucher No", Align: printing.AlignLeft, Width: 2},
		{Header: "Type", Align: printing.AlignLeft, Width: 2.5},
		{Header: "Class", Align: printing.AlignLeft, Width: 2},
		// THE OPERATION, and it is not padding. Every row is the voucher's CURRENT
		// version, so this column says whether the document an operator is looking
		// at is as first keyed (CREATE) or has been RESTATED since (ALTER) — which
		// is the difference between "this is what was agreed" and "this was changed
		// after the fact", and the only place on the page it can be read.
		{Header: "Op", Align: printing.AlignLeft, Width: 1.5},
		{Header: "Party", Align: printing.AlignLeft, Width: 3},
		{Header: "Narration", Align: printing.AlignLeft, Width: 5},
		{Header: "Amount", Align: printing.AlignRight, Width: 2.5},
	}

	rows := make([]printing.Row, 0, len(dbk.Entries))
	for _, e := range dbk.Entries {
		// Debit and Credit are equal by double entry (reporting surfaces both so a
		// UI can show either), so ONE amount column is printed and it is the
		// debit total. Printing two identical columns invites a reader to add them.
		rows = append(rows, printing.Row{Cells: []string{
			printISODate(e.Date), e.VoucherNo, e.VoucherTypeName, e.Class,
			e.Op, e.Party, e.Narration, printMoney(e.Debit),
		}})
	}

	// ⛔ DELIBERATELY NO COLUMN TOTAL, AND THE FOOTER HAD TO SAY SO TRUTHFULLY.
	//
	// It used to say "an altered voucher appears as both its original and its
	// restatement, so a sum of the amounts would count the edit twice", and that
	// was FALSE FOR THIS BUILD: reporting.DayBook joins voucher_head on
	// current_version_id and filters is_deleted, so the report is exactly ONE ROW
	// PER LIVE VOUCHER — no superseded version, no tombstone. A footer explaining
	// the absence of a total with a reason that does not apply teaches an operator
	// something wrong about their own Day Book, and it is the only sentence on the
	// page that explains anything.
	//
	// The real reason a total would be meaningless is stronger, not weaker: these
	// rows are vouchers of every CLASS, so a sale and the receipt that settles it
	// both appear, and adding them counts the same money twice. That is a fact
	// about double entry, not about editing.
	//
	// ⛔ AND IT IS PLACED WITH printNoteRows RATHER THAN IN CELL 0. The old footer
	// was one long cell in the 2-wide "Date" column, so the renderer wrapped it to
	// four lines and cut it off mid-sentence at "No column total is..." — present
	// on the page, unreadable, and unnoticed because nobody re-reads a footer.
	footer := []printing.Row{{
		Cells:     []string{strconv.Itoa(len(dbk.Entries)) + " voucher(s) in this period"},
		Bold:      true,
		RuleAbove: true,
	}}
	footer = append(footer, printNoteRows(cols, true,
		"One line per voucher, at its CURRENT version: a voucher altered since it was "+
			"keyed appears once, restated, and is marked ALTER in the Op column; a deleted "+
			"voucher does not appear at all. No column total is printed — these rows are "+
			"vouchers of every class, so a sale and the receipt that settles it both appear "+
			"here, and adding them would count the same money twice.")...)

	return printing.ReportTable{
		Title:     "Day Book",
		Subtitle:  printPeriodSubtitle(dbk.From, dbk.To),
		Landscape: true,
		Columns:   cols,
		Rows:      rows,
		Footer:    footer,
	}, true
}

// ---------------------------------------------------------------------------
// 5. Outstandings (receivables / payables ageing)
// ---------------------------------------------------------------------------

// outstandingKindParam parses the `kind` query param.
//
// EXTRACTED FROM handleOutstandings AND SHARED WITH IT ON PURPOSE. If the printed
// ageing parsed `kind` even slightly differently from the on-screen one — say it
// defaulted to payables, or accepted "Receivable" where the JSON handler did not
// — an operator would chase a customer for a figure that is actually a supplier
// balance. One parser, one answer.
func outstandingKindParam(w http.ResponseWriter, r *http.Request) (reporting.OutstandingKind, bool) {
	switch r.URL.Query().Get("kind") {
	case "", "RECEIVABLE", "receivable":
		return reporting.Receivables, true
	case "PAYABLE", "payable":
		return reporting.Payables, true
	default:
		writeError(w, http.StatusBadRequest, "invalid kind (RECEIVABLE|PAYABLE)")
		return "", false
	}
}

// outstandingsSubtitle is the ONE writing of the printed ageing's scope sentence.
//
// ⚠️ It takes the whole report rather than two strings so a caller cannot pass the
// ageing date and forget the bound — which is exactly the mistake D86 was.
func outstandingsSubtitle(out reporting.Outstanding) string {
	aged := "Bill-wise, aged " + printAsOnSubtitle(out.AsOn)
	if out.Bounded {
		return aged
	}
	return aged + " — NO DATE LIMIT on which bills are listed: every bill on these " +
		"books, including any dated after that day"
}

func (s *Server) printOutstandings(ctx context.Context, w http.ResponseWriter, r *http.Request, companyID uuid.UUID) (printing.ReportTable, bool) {
	kind, ok := outstandingKindParam(w, r)
	if !ok {
		return printing.ReportTable{}, false
	}
	// ⛔⛔ THE PAPER DOOR IS RETIRED TOO, AND CLOSING ONLY THE JSON ONE WOULD HAVE
	// BEEN THE §5.19 MISTAKE EXACTLY (D97). This route is decided as
	// `print report:outstandings`; serving the payables list from it handed a level
	// refused `print report:bills-payable` the same rows on a filed sheet, which is
	// the leak wearing a letterhead. Same 308 as the JSON handler, for the same
	// reasons written there — the redirected request is decided as
	// `print report:bills-payable`, by the middleware, where every refusal belongs.
	if kind == reporting.Payables {
		q := r.URL.Query()
		q.Del("kind")
		target := strings.TrimSuffix(r.URL.EscapedPath(), "/outstandings") + "/bills-payable"
		if enc := q.Encode(); enc != "" {
			target += "?" + enc
		}
		http.Redirect(w, r, target, http.StatusPermanentRedirect)
		return printing.ReportTable{}, false
	}
	return s.printOutstandingsOfKind(ctx, w, r, companyID, kind)
}

// printOutstandingsOfKind is the ONE printed layout behind both ageing report names.
//
// ⚠️ The kind is a PARAMETER rather than re-parsed, so `/print/reports/bills-payable`
// cannot be talked into printing the receivables list by a `?kind=RECEIVABLE` on the
// URL — which would hand a caller that list from under the payables print rule.
func (s *Server) printOutstandingsOfKind(ctx context.Context, w http.ResponseWriter, r *http.Request, companyID uuid.UUID, kind reporting.OutstandingKind) (printing.ReportTable, bool) {
	asOn := r.URL.Query().Get("as_on")
	out, err := s.report.Outstandings(ctx, companyID, kind, asOn, nil)
	if err != nil {
		// The JSON handler answers 422 here (an unparseable as_on reaches
		// reporting.resolveAsOn, not reportDate), and the printed copy matches it
		// rather than inventing a different status for the same input.
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return printing.ReportTable{}, false
	}

	var rows []printing.Row
	for _, p := range out.Parties {
		rows = append(rows, printing.Row{
			Cells: []string{p.LedgerName, p.GroupName, "", "", "", "", "", printMoney(p.Pending)},
			Bold:  true,
		})
		for _, b := range p.Bills {
			// ⭐⭐ T8 STEP 6 — THE FOREIGN COLUMN IS ON THE PAPER TOO, AND THAT IS NOT
			// OPTIONAL. D20's defect was two surfaces over one fact drifting apart;
			// a printed Outstandings that showed only the rupee figure would be a
			// statement an operator posts to a customer, of a dollar debt, with the
			// currency silently dropped. ⚠️ THE PARTY ROW LEAVES IT BLANK rather than
			// totalling it, for Q5's reason: mixed currencies do not add up.
			//
			// ⛔ AND THE CONFLICT IS MARKED ON THE ROW, not left to the footer.
			// Two rows under one bill number on paper, with nothing between them,
			// read as two bills — and paper has no tooltip to explain otherwise.
			foreign := ""
			if b.CurrencyCode != "" {
				foreign = b.CurrencyCode + " " + printMoney(b.ForeignPending)
			}
			ref := b.RefName
			if b.CurrencyConflict {
				ref += " (!) two currencies"
			}
			rows = append(rows, printing.Row{
				Cells: []string{"", "", ref, printISODate(b.DueDate),
					// Days overdue is a COUNT, not money: strconv, and never
					// FormatMoney, which would print "45.00 days".
					strconv.Itoa(b.OverdueDays), b.SlabLabel, foreign, printMoney(b.Pending)},
				Indent: 1,
			})
		}
	}

	var footer []printing.Row
	for _, st := range out.SlabTotals {
		footer = append(footer, printing.Row{
			Cells: []string{"Ageing " + st.Label, "", "", "", "", "", "", printMoney(st.Amount)},
		})
	}
	// ⚠️ THE SLABS AND THE TOTAL ARE BASE-ONLY AND THE FOREIGN CELL IS LEFT EMPTY,
	// which is Q5's explicit design decision rather than an omission: a total that
	// added USD to INR would be a figure in no currency that nonetheless foots.
	footer = append(footer, printing.Row{
		Cells:     []string{"Total outstanding", "", "", "", "", "", "", printMoney(out.Total)},
		Bold:      true,
		RuleAbove: true,
	})

	title := "Outstanding Receivables"
	if kind == reporting.Payables {
		title = "Outstanding Payables"
	}
	return printing.ReportTable{
		Title: title,
		// ⛔ THE PRINTED COPY MUST NOT CLAIM A BOUND THE ROWS DO NOT HAVE (D86). The
		// ageing sentence is true either way — every bill is aged against `AsOn` — but
		// on an unbounded read the sheet also has to say the ROWS are not date-limited,
		// or a printout filed against a quarter looks like a quarter-end statement and
		// is not one. Same words the screen uses, so the paper and the screen cannot
		// disagree.
		Subtitle:  outstandingsSubtitle(out),
		Landscape: true,
		Columns: []printing.Column{
			{Header: "Party", Align: printing.AlignLeft, Width: 4},
			{Header: "Group", Align: printing.AlignLeft, Width: 3},
			{Header: "Bill Ref", Align: printing.AlignLeft, Width: 3},
			{Header: "Due Date", Align: printing.AlignLeft, Width: 2},
			{Header: "Days Overdue", Align: printing.AlignRight, Width: 1.6},
			{Header: "Ageing", Align: printing.AlignLeft, Width: 2},
			// ⭐ T8 STEP 6. Blank on a base bill, and it is a SEPARATE column from
			// Pending — never the same one. Two different things must never share a
			// column, and on a printed sheet the reader cannot ask which this is.
			{Header: "Foreign", Align: printing.AlignRight, Width: 2},
			{Header: "Pending", Align: printing.AlignRight, Width: 2.4},
		},
		Rows:   rows,
		Footer: footer,
	}, true
}

// ---------------------------------------------------------------------------
// 6. Stock Summary
// ---------------------------------------------------------------------------

func (s *Server) printStockSummary(ctx context.Context, w http.ResponseWriter, r *http.Request, companyID uuid.UUID) (printing.ReportTable, bool) {
	asOn, ok := reportDate(w, r, "as_on")
	if !ok {
		return printing.ReportTable{}, false
	}
	sum, err := s.report.StockSummary(ctx, companyID, asOn)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return printing.ReportTable{}, false
	}

	var rows []printing.Row
	bases := map[string]bool{}
	// ⛔ THE SET OF BASES IS COMPUTED BEFORE THE ROWS, AND THAT ORDER IS THE
	// FEATURE (T9). The footer has always named the basis this report was derived
	// on, which was enough while WEIGHTED_AVG was the only one. Costing method is
	// a property of the ITEM, so one company's Stock Summary can now mix three of
	// them — and a footer reading "Valuation basis: FIFO, WEIGHTED_AVG" over a
	// page of item totals tells a reader that two bases are present and NOT which
	// item is on which, while every figure is confidently printed.
	//
	// So when (and only when) more than one basis appears, each item line names
	// its own. A single-basis report — every existing company, and every existing
	// print golden — is unchanged to the byte: the footer already says it once,
	// and repeating it on every line would be noise.
	for _, it := range sum.Items {
		bases[it.ValuationMethod] = true
	}
	mixed := len(bases) > 1
	for _, it := range sum.Items {
		itemLabel := it.ItemName
		if mixed {
			itemLabel += " · " + it.ValuationMethod
		}
		// T10 — a COMPOUND unit's closing quantity is restated in its base unit,
		// IN THE CELL rather than in a new column. The paper is read, not summed
		// (the CSV export is the one that has to stay numeric, and it gets its own
		// column), and a twelfth column on a landscape page costs every other
		// column width for a figure that appears on the few items that have one.
		closingQty := printing.FormatQty(it.ClosingQty)
		if it.BaseUnitSymbol != "" && it.ConversionFactor.IsPositive() {
			closingQty += " (= " + printing.FormatQty(it.ClosingBaseQty) + " " + it.BaseUnitSymbol + ")"
		}
		rows = append(rows, printing.Row{
			Cells: []string{itemLabel, it.UnitSymbol,
				printing.FormatQty(it.OpeningQty), printMoneyOrBlank(it.OpeningValue),
				printing.FormatQty(it.InwardQty), printMoneyOrBlank(it.InwardValue),
				printing.FormatQty(it.OutwardQty), printMoneyOrBlank(it.OutwardValue),
				closingQty, printMoney(it.ClosingValue)},
			Bold: true,
		})
		for _, p := range it.Positions {
			// The item's own basis was already folded in above; a position
			// cannot carry a different one (the method is on the item), and
			// folding it here as well would let the two disagree silently.
			rows = append(rows, printing.Row{
				Cells: []string{printStockPositionLabel(p), p.UnitSymbol,
					printing.FormatQty(p.OpeningQty), printMoneyOrBlank(p.OpeningValue),
					printing.FormatQty(p.InwardQty), printMoneyOrBlank(p.InwardValue),
					printing.FormatQty(p.OutwardQty), printMoneyOrBlank(p.OutwardValue),
					printing.FormatQty(p.ClosingQty), printMoney(p.ClosingValue)},
				Indent: 1,
			})
		}
	}

	// ⛔ TEN COLUMNS, AND EVERY QUANTITY HAS ITS VALUE BESIDE IT. Do not "simplify"
	// this to match the on-screen Stock Summary, which shows seven and puts a
	// QUANTITY under "Inward" immediately beside a MONEY figure under "Outward (at
	// cost)". The paper is the side that is right: a movement report that shows
	// what came in as units and what went out as rupees cannot be read down either
	// column, and the reader has no way to see which is which. The screen is the
	// one that has to change.
	cols := []printing.Column{
		{Header: "Item / Location", Align: printing.AlignLeft, Width: 4},
		{Header: "Unit", Align: printing.AlignLeft, Width: 1},
		{Header: "Opening Qty", Align: printing.AlignRight, Width: 1.8},
		{Header: "Opening Value", Align: printing.AlignRight, Width: 2.2},
		{Header: "Inward Qty", Align: printing.AlignRight, Width: 1.8},
		{Header: "Inward Value", Align: printing.AlignRight, Width: 2.2},
		{Header: "Outward Qty", Align: printing.AlignRight, Width: 1.8},
		{Header: "Outward Value", Align: printing.AlignRight, Width: 2.2},
		{Header: "Closing Qty", Align: printing.AlignRight, Width: 1.8},
		{Header: "Closing Value", Align: printing.AlignRight, Width: 2.2},
	}

	footer := []printing.Row{{
		Cells: []string{"Closing value of the whole inventory", "", "", "", "", "", "", "", "",
			printMoney(sum.TotalValue)},
		Bold:      true,
		RuleAbove: true,
	}}
	// A statement must be able to state its own basis. Nothing on this report is
	// stored — every figure is a replay of the movement ledger through a valuer —
	// so a reader who cannot see WHICH valuer ran cannot check any of it.
	//
	// Placed with printNoteRows: cell 0 here is the 4-wide "Item / Location"
	// column, which holds about thirty characters a line, so this sentence was
	// being cut off with an ellipsis at exactly the point where it names the
	// valuer's consequence.
	if b := printSortedKeys(bases); b != "" {
		footer = append(footer, printNoteRows(cols, true, "Valuation basis: "+b+
			". Value is never stored; every figure above is a replay of the movement ledger.")...)
	}

	// T10 — THE STOCK GROUP SUBTOTALS, as notes rather than as rows in the table.
	//
	// ⛔ THEY ARE NOT TABLE ROWS BECAUSE THEY DO NOT BELONG TO ITS ARITHMETIC:
	// every item counts in every ancestor of its group, so a group line placed in
	// the body would sit in a column that a reader adds down — and adding it would
	// double-count every nested branch. Stated as notes, with the reason, they are
	// a summary of the page rather than part of its total.
	//
	// ⚠️ A company with no groups prints nothing here (grouped is empty), rather
	// than a note saying every item is ungrouped.
	var grouped []reporting.StockGroupRollup
	for _, g := range sum.Groups {
		if g.GroupName != "" {
			grouped = append(grouped, g)
		}
	}
	if len(grouped) > 0 {
		note := "Stock group subtotals (the whole branch, so they OVERLAP and must not be added): "
		for i, g := range grouped {
			if i > 0 {
				note += "; "
			}
			note += g.GroupName + " " + printMoney(g.ClosingValue)
		}
		footer = append(footer, printNoteRows(cols, false, note)...)
	}

	return printing.ReportTable{
		Title:     "Stock Summary",
		Subtitle:  printAsOnSubtitle(sum.AsOn),
		Landscape: true,
		Columns:   cols,
		Rows:      rows,
		Footer:    footer,
	}, true
}

// printStockPositionLabel names one (godown, batch) position. An untracked
// dimension is a POSITION like any other — a company that never enabled
// multi-location keeps all its stock there — so it is labelled rather than left
// blank, which would read as a rendering fault.
func printStockPositionLabel(p reporting.StockPositionRow) string {
	parts := make([]string, 0, 2)
	if p.GodownName != "" {
		parts = append(parts, p.GodownName)
	}
	if p.BatchName != "" {
		parts = append(parts, "batch "+p.BatchName)
	}
	if len(parts) == 0 {
		return "(no location or batch recorded)"
	}
	return strings.Join(parts, " / ")
}

// printSortedKeys joins a set's keys in a stable order, so the same books print
// the same bytes twice — which is what makes a golden test on a report possible.
func printSortedKeys(set map[string]bool) string {
	keys := make([]string, 0, len(set))
	for k := range set {
		if k != "" {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return ""
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return strings.Join(keys, ", ")
}

// ---------------------------------------------------------------------------
// 7. Payroll Register
// ---------------------------------------------------------------------------

func (s *Server) printPayrollRegister(ctx context.Context, w http.ResponseWriter, r *http.Request) (printing.ReportTable, bool) {
	// The SAME strict wage-month parser the JSON handler uses. "2026-13" and
	// "2026-1" are both refused: a month silently coerced to a neighbouring one
	// computes somebody's contributions under the wrong statutory window.
	month, ok := wageMonthParam(w, r)
	if !ok {
		return printing.ReportTable{}, false
	}
	reg, err := s.payroll.PayrollRegister(ctx, month)
	if err != nil {
		writePayrollError(w, err)
		return printing.ReportTable{}, false
	}

	// Hoisted so the footer notes can be laid out against them (printNoteRows).
	cols := []printing.Column{
		{Header: "Code", Align: printing.AlignLeft, Width: 2.2},
		{Header: "Employee", Align: printing.AlignLeft, Width: 4},
		{Header: "Days", Align: printing.AlignRight, Width: 1.2},
		{Header: "Gross", Align: printing.AlignRight, Width: 2},
		{Header: "EPF (empl)", Align: printing.AlignRight, Width: 2},
		{Header: "ESI (empl)", Align: printing.AlignRight, Width: 2},
		{Header: "TDS", Align: printing.AlignRight, Width: 2},
		{Header: "Deductions", Align: printing.AlignRight, Width: 2},
		{Header: "Net Pay", Align: printing.AlignRight, Width: 2},
		{Header: "EPF (emplr)", Align: printing.AlignRight, Width: 2},
		{Header: "EPS (emplr)", Align: printing.AlignRight, Width: 2},
		{Header: "ESI (emplr)", Align: printing.AlignRight, Width: 2},
	}

	rows := make([]printing.Row, 0, len(reg.Rows))
	for _, row := range reg.Rows {
		rows = append(rows, printing.Row{Cells: []string{
			row.EmployeeCode, row.EmployeeName,
			printing.FormatQty(row.DaysPaid),
			printMoney(row.Gross),
			printMoneyOrBlank(row.EPFEmployee), printMoneyOrBlank(row.ESIEmployee),
			printMoneyOrBlank(row.TDS), printMoney(row.Deductions), printMoney(row.Net),
			printMoneyOrBlank(row.EPFEmployer), printMoneyOrBlank(row.EPSEmployer),
			printMoneyOrBlank(row.ESIEmployer),
		}})
	}

	footer := []printing.Row{{
		Cells: []string{"", "Total",
			printing.FormatQty(reg.Totals.DaysPaid),
			printMoney(reg.Totals.Gross),
			printMoney(reg.Totals.EPFEmployee), printMoney(reg.Totals.ESIEmployee),
			printMoney(reg.Totals.TDS), printMoney(reg.Totals.Deductions), printMoney(reg.Totals.Net),
			printMoney(reg.Totals.EPFEmployer), printMoney(reg.Totals.EPSEmployer),
			printMoney(reg.Totals.ESIEmployer)},
		Bold:      true,
		RuleAbove: true,
	}}
	// THE RULESET VERSIONS GO ON THE PAPER. A payroll figure with no statement of
	// which law produced it cannot be checked by anybody who was not present when
	// the run happened, and a register is exactly the document somebody checks a
	// year later.
	//
	// ⛔ IN THE WIDE COLUMN, NOT IN CELL 0. Cell 0 here is the 2.2-wide "Code"
	// column: the second sentence below is 120 characters and was being wrapped to
	// four short lines and then cut off, so the one statement on the page that
	// stops an employer contribution being read as part of somebody's pay was
	// unreadable.
	footer = append(footer, printNoteRows(cols, true,
		"Social-security ruleset: "+reg.SSRuleSetVersion+
			"   Income-tax ruleset: "+reg.ITRuleSetVersion)...)
	footer = append(footer, printNoteRows(cols, true,
		"The employer's contributions are NOT part of gross pay and are "+
			"not included in it: they are a separate cost to the company.")...)

	return printing.ReportTable{
		Title:     "Payroll Register",
		Subtitle:  "Wage month " + reg.WageMonth + " — pay run " + reg.Status,
		Landscape: true,
		Columns:   cols,
		Rows:      rows,
		Footer:    footer,
	}, true
}

// ---------------------------------------------------------------------------
// 9. Bank Reconciliation Statement
// ---------------------------------------------------------------------------

// bankReconPrintScope resolves WHICH bank accounts this request is for.
//
// Returns the ledger ids to print, in the order they will appear. See the file
// header for why a bare request is a 400 rather than a default.
func (s *Server) bankReconPrintScope(
	ctx context.Context, w http.ResponseWriter, r *http.Request,
) ([]uuid.UUID, bool) {
	q := r.URL.Query()
	rawLedger := strings.TrimSpace(q.Get("ledger_id"))
	all := q.Get("all") == "true"

	switch {
	case rawLedger == "" && !all:
		// The original refusal's reasoning, preserved as the case it was actually
		// about. It names both ways forward rather than only reporting the fault.
		writeError(w, http.StatusBadRequest,
			"a Bank Reconciliation Statement is always for ONE bank account, and this "+
				"request did not say which: pass ?ledger_id=<uuid> for a single account, "+
				"or ?all=true to print every bank account in one document. Printing a "+
				"statement for an account you did not choose would put the wrong account "+
				"on paper with nothing on the page to say so")
		return nil, false
	case rawLedger != "" && all:
		writeError(w, http.StatusBadRequest,
			"pass either ?ledger_id=<uuid> or ?all=true, not both: they are two "+
				"different documents and the server cannot tell which one was meant")
		return nil, false
	case rawLedger != "":
		id, err := uuid.Parse(rawLedger)
		if err != nil {
			writeError(w, http.StatusBadRequest, "ledger_id must be a uuid")
			return nil, false
		}
		// NOT validated as a bank ledger here. BankReconciliationStatement refuses
		// a non-bank ledger BY NAME (bankrecon.go's diagnostic-refusal habit), and
		// duplicating the check would give two different sentences for one fault.
		return []uuid.UUID{id}, true
	}

	// ?all=true. The bank-ness test is reporting.BankLedgerPredicate — the SAME
	// expression the ledger list and the statement endpoint use. It is ltree
	// ANCESTRY, not the immediate group name: a ledger in a user-created "Current
	// Accounts" sub-group under the seeded "Bank Accounts" primary is a bank
	// account, and a name comparison would silently omit it from the document.
	var ids []uuid.UUID
	err := s.pool.RunInReadOnlyTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT l.id
			   FROM ledger l
			   JOIN account_group g ON g.id = l.group_id
			  WHERE `+reporting.BankLedgerPredicate+`
			  ORDER BY g.name, l.name`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return rows.Err()
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list bank ledgers: "+err.Error())
		return nil, false
	}
	if len(ids) == 0 {
		// An empty PDF headed "Bank Reconciliation Statement" reads as "your banks
		// reconcile to nothing", which is a different and alarming claim.
		writeError(w, http.StatusNotFound,
			"this company has no bank accounts, so there is no Bank Reconciliation "+
				"Statement to print. A ledger is a bank account when its group sits under "+
				"'Bank Accounts' or 'Bank OD A/c'")
		return nil, false
	}
	return ids, true
}

// printBankReconciliation renders one section per requested bank account.
//
// THE ARITHMETIC IS NOT RE-DERIVED HERE. Every figure comes from the same
// reporting.BankReconciliationStatement the screen calls (rule 1 of this file's
// header), including Difference, which the report computes so it can assert its
// own identity rather than have a printer assume it.
func (s *Server) printBankReconciliation(
	ctx context.Context, w http.ResponseWriter, r *http.Request, companyID uuid.UUID,
) (printing.ReportTable, bool) {
	asOn, ok := reportDate(w, r, "as_on")
	if !ok {
		return printing.ReportTable{}, false
	}
	ledgerIDs, ok := s.bankReconPrintScope(ctx, w, r)
	if !ok {
		return printing.ReportTable{}, false
	}

	var (
		rows        []printing.Row
		names       []string
		anyUnbal    bool
		totalBroken int
	)
	for i, ledgerID := range ledgerIDs {
		brs, err := s.report.BankReconciliationStatement(ctx, companyID, ledgerID, asOn)
		if err != nil {
			// The service's own sentence — it names the ledger and says whether the
			// fault is "no such ledger" or "not a bank account". 422 matches the
			// JSON handler for the same input rather than inventing a new status.
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return printing.ReportTable{}, false
		}
		names = append(names, brs.LedgerName)
		if !brs.IsBalanced() {
			anyUnbal = true
		}
		totalBroken += brs.BrokenCount
		if i > 0 {
			rows = append(rows, printing.Row{Cells: []string{"", "", "", "", ""}})
		}
		rows = append(rows, printBankReconSection(brs)...)
	}

	// The footer is about the DOCUMENT, so it only earns its place when the
	// document spans more than one account or something is wrong.
	//
	// Every note goes in the PARTICULARS column via bankNote, never in cell 0.
	// layoutRows confines a cell to its own column — there is no spanning — so a
	// sentence placed in the 2-wide "Date" column is wrapped to four lines and
	// then truncated with an ellipsis. The note would still be on the page and
	// would no longer be readable, which is the worst of both.
	var footer []printing.Row
	if len(ledgerIDs) > 1 {
		footer = append(footer, printing.Row{
			Cells:     []string{"", "", strconv.Itoa(len(ledgerIDs)) + " bank accounts in this document", "", ""},
			Bold:      true,
			RuleAbove: true,
		})
		// DELIBERATELY NO GRAND TOTAL ACROSS ACCOUNTS. Summing several banks'
		// closing balances produces a number that is not on any statement and
		// ties to nothing an operator can check against a passbook — and if one
		// account is an OD (a liability) the sum is not even meaningful.
		footer = append(footer, bankNote(
			"No combined total is printed: each statement reconciles to its own bank."))
	}
	if totalBroken > 0 {
		footer = append(footer, bankNote(
			strconv.Itoa(totalBroken)+" cleared item(s) no longer match their voucher and are "+
				"counted as UN-CLEARED above. Re-clear them against the bank statement."))
	}
	if anyUnbal {
		footer = append(footer, printing.Row{
			Cells: []string{"", "", "A STATEMENT IN THIS DOCUMENT DOES NOT RECONCILE. Do not sign it.", "", ""},
			Bold:  true,
		})
	}

	title := "Bank Reconciliation Statement"
	subtitle := printAsOnSubtitle(asOn)
	if len(names) == 1 {
		subtitle = names[0] + " — " + subtitle
	} else {
		subtitle = "All bank accounts — " + subtitle
	}
	return printing.ReportTable{
		Title:     title,
		Subtitle:  subtitle,
		Landscape: true,
		Columns: []printing.Column{
			{Header: "Date", Align: printing.AlignLeft, Width: 2},
			{Header: "Voucher No", Align: printing.AlignLeft, Width: 2},
			{Header: "Particulars", Align: printing.AlignLeft, Width: 7},
			{Header: "Days", Align: printing.AlignRight, Width: 1.2},
			{Header: "Amount", Align: printing.AlignRight, Width: 2.5},
		},
		Rows:   rows,
		Footer: footer,
	}, true
}

// bankNote is a note row on the BRS, placed in the PARTICULARS column.
//
// It exists instead of printLabelRow because printLabelRow puts its text in
// cell 0, and on this table cell 0 is the 2-wide "Date" column. There is no
// column spanning in layoutRows, so a sentence there is wrapped to
// reportMaxCellLines and then ellipsised — present on the page and unreadable.
func bankNote(text string) printing.Row {
	return printing.Row{Cells: []string{"", "", text, "", ""}}
}

// printBankReconSection is one account's statement, in the order a reader
// checks it: the book balance, the items the bank has not seen, the bank
// balance, and the residual.
func printBankReconSection(brs reporting.BankReconciliation) []printing.Row {
	out := []printing.Row{{
		Cells:     []string{brs.LedgerName, "", "", "", ""},
		Bold:      true,
		RuleBelow: true,
	}}
	out = append(out, printing.Row{
		Cells: []string{"", "", "Balance as per company books", "", printMoney(brs.BalanceAsPerBooks)},
		Bold:  true,
	})

	if len(brs.Uncleared) == 0 {
		out = append(out, printing.Row{
			Cells:  []string{"", "", "No items are awaiting clearance.", "", ""},
			Indent: 1,
		})
	} else {
		out = append(out, printing.Row{
			Cells:  []string{"", "", "Less: amounts not yet reflected in the bank", "", ""},
			Indent: 1,
		})
		for _, l := range brs.Uncleared {
			out = append(out, printing.Row{
				Cells: []string{
					printISODate(l.VoucherDate), l.VoucherNo,
					printBankReconParticulars(l),
					// Age is a COUNT of days: strconv, never FormatMoney, which
					// would render "45.00 days".
					strconv.Itoa(l.AgeDays),
					// Dr/Cr rides in the particulars, not in a sign. Amount is a
					// magnitude everywhere in this codebase and printing a signed
					// figure here would contradict every other statement.
					printMoney(l.Amount),
				},
				Indent: 2,
			})
		}
		out = append(out, printing.Row{
			Cells:     []string{"", "", "Net of items awaiting clearance", "", printMoney(brs.UnclearedNet)},
			RuleAbove: true,
		})
	}

	out = append(out, printing.Row{
		Cells:     []string{"", "", "Balance as per bank", "", printMoney(brs.BalanceAsPerBank)},
		Bold:      true,
		RuleAbove: true,
	})
	// The residual is printed even when it is zero, and that is the point: a
	// reader must see that the statement asserted its own arithmetic rather
	// than infer it from the absence of a line.
	out = append(out, printing.Row{
		Cells: []string{"", "", "Difference", "", printMoney(brs.Difference)},
		Bold:  !brs.IsBalanced(),
	})
	if !brs.IsBalanced() {
		out = append(out, printing.Row{
			Cells: []string{"", "", "THIS STATEMENT DOES NOT RECONCILE.", "", ""},
			Bold:  true,
		})
	}
	return out
}

// printBankReconParticulars words one un-cleared line.
//
// The Dr/Cr is spelled as what it MEANS on a bank account — a debit to the bank
// ledger is money received — because "Dr" against a bank line is the single
// most reliably misread abbreviation on a reconciliation.
func printBankReconParticulars(l reporting.BankReconLine) string {
	var b strings.Builder
	if l.DrCr == "DR" {
		b.WriteString("Received")
	} else {
		b.WriteString("Paid")
	}
	if l.Party != "" {
		b.WriteString(" — ")
		b.WriteString(l.Party)
	}
	if l.Narration != "" {
		b.WriteString(" (")
		b.WriteString(l.Narration)
		b.WriteString(")")
	}
	if l.BrokenByAlter {
		// The evidence, on the paper. An item whose voucher was altered after
		// clearing is counted as un-cleared, and a reader has to be able to see
		// WHY this line is here when the screen says it was reconciled.
		b.WriteString("  [BROKEN: cleared as ")
		b.WriteString(printing.FormatMoney(l.ClearedAmount))
		b.WriteString(" ")
		b.WriteString(l.ClearedDrCr)
		b.WriteString(", voucher since altered]")
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// 8. Payslip
// ---------------------------------------------------------------------------

func (s *Server) printPayrollPayslip(ctx context.Context, w http.ResponseWriter, r *http.Request) (printing.ReportTable, bool) {
	month, ok := wageMonthParam(w, r)
	if !ok {
		return printing.ReportTable{}, false
	}
	// The same required employee_id the JSON handler demands. An id belonging to
	// another company resolves, through RLS, to no rows and is answered 404 by
	// writePayrollError — there is no path by which this reads outside the
	// authorized company.
	employeeID, err := uuid.Parse(r.URL.Query().Get("employee_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "employee_id query param is required and must be a uuid")
		return printing.ReportTable{}, false
	}
	v, err := s.payroll.Payslip(ctx, month, employeeID)
	if err != nil {
		writePayrollError(w, err)
		return printing.ReportTable{}, false
	}

	rows := make([]printing.Row, 0, len(v.Lines))
	for _, l := range v.Lines {
		earn, ded := "", ""
		if l.Direction == "DEDUCTION" {
			ded = printMoney(l.Amount)
		} else {
			earn = printMoney(l.Amount)
		}
		rows = append(rows, printing.Row{Cells: []string{l.Code, l.Type, earn, ded}})
	}

	footer := []printing.Row{
		{Cells: []string{"Gross earnings", "", printMoney(v.Gross), ""}, Bold: true, RuleAbove: true},
		{Cells: []string{"Total deductions", "", "", printMoney(v.Deductions)}, Bold: true},
		{Cells: []string{"NET PAY", "", printMoney(v.Net), ""}, Bold: true, RuleAbove: true},
		printLabelRow("Days paid " + printing.FormatQty(v.DaysPaid) +
			"   Loss of pay " + printing.FormatQty(v.LOPDays)),
	}
	// THE DEFERRALS ARE ON THE PAYSLIP, not in a log. A blank where a number
	// should be is a question the employee will ask, and the honest answer is
	// "the rate has not been notified", never "zero".
	for _, d := range v.Deferrals {
		footer = append(footer, printLabelRow("Not computed — "+d.Component+": "+d.Reason+" ("+d.Citation+")"))
	}
	footer = append(footer, printLabelRow("Social-security ruleset: "+v.SSRuleSetVersion+
		"   Income-tax ruleset: "+v.ITRuleSetVersion))

	return printing.ReportTable{
		Title:    "Payslip",
		Subtitle: v.EmployeeName + " (" + v.EmployeeCode + ") — wage month " + v.WageMonth,
		Columns: []printing.Column{
			{Header: "Component", Align: printing.AlignLeft, Width: 4},
			{Header: "Type", Align: printing.AlignLeft, Width: 2},
			{Header: "Earnings", Align: printing.AlignRight, Width: 2},
			{Header: "Deductions", Align: printing.AlignRight, Width: 2},
		},
		Rows:   rows,
		Footer: footer,
	}, true
}
