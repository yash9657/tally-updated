package server

// handler_print_reports.go — GET /v1/companies/{companyID}/print/reports/{name}
//
// EIGHT reports print for real:
//
//	trial-balance   balance-sheet   profit-and-loss   day-book
//	outstandings    stock-summary   payroll-register  payroll-payslip
//
// EVERY OTHER NAME IN authz.KnownReportNames STAYS MAPPED AND ANSWERS 501. The
// deferred set is, in full:
//
//	cash-flow, anomalies, gstr1, gstr3b, reconciliation-queue,
//	stock-movement, godown-summary, batch-summary, stock-item-vouchers,
//	itc-reconciliation, ims-inbox,
//	payroll-form-138, payroll-pf-ecr, payroll-esi-return
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
	case "stock-summary":
		table, built = s.printStockSummary(ctx, w, r, companyID)
	case "payroll-register":
		table, built = s.printPayrollRegister(ctx, w, r)
	case "payroll-payslip":
		table, built = s.printPayrollPayslip(ctx, w, r)
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

// printLabelRow is a full-width note row: one cell, the rest padded by the
// renderer (layoutRows pads a short row and errors only on a LONG one).
func printLabelRow(text string) printing.Row {
	return printing.Row{Cells: []string{text}}
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

	return printing.ReportTable{
		Title:     "Day Book",
		Subtitle:  printPeriodSubtitle(dbk.From, dbk.To),
		Landscape: true,
		Columns: []printing.Column{
			{Header: "Date", Align: printing.AlignLeft, Width: 2},
			{Header: "Voucher No", Align: printing.AlignLeft, Width: 2},
			{Header: "Type", Align: printing.AlignLeft, Width: 2.5},
			{Header: "Class", Align: printing.AlignLeft, Width: 2},
			// The OPERATION, and it is not padding. DayBook lists every posted
			// VERSION as its own dated event, so an ALTER restatement and a DELETE
			// tombstone each appear as a line. Without this column an alter reads
			// as a second, separate voucher for the same number.
			{Header: "Op", Align: printing.AlignLeft, Width: 1.5},
			{Header: "Party", Align: printing.AlignLeft, Width: 3},
			{Header: "Narration", Align: printing.AlignLeft, Width: 5},
			{Header: "Amount", Align: printing.AlignRight, Width: 2.5},
		},
		Rows: rows,
		// DELIBERATELY NO COLUMN TOTAL. The entries are voucher VERSIONS: an
		// altered voucher contributes its original and its restatement, and a
		// deleted one contributes a tombstone. A sum of the amount column would
		// therefore double-count every edit in the period and print a "total"
		// that ties to nothing in the ledger. The count is the honest footer.
		Footer: []printing.Row{{
			Cells: []string{strconv.Itoa(len(dbk.Entries)) + " voucher version(s) in this period. " +
				"No column total is printed: an altered voucher appears as both its original " +
				"and its restatement, so a sum of the amounts would count the edit twice."},
			Bold:      true,
			RuleAbove: true,
		}},
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

func (s *Server) printOutstandings(ctx context.Context, w http.ResponseWriter, r *http.Request, companyID uuid.UUID) (printing.ReportTable, bool) {
	kind, ok := outstandingKindParam(w, r)
	if !ok {
		return printing.ReportTable{}, false
	}
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
			Cells: []string{p.LedgerName, p.GroupName, "", "", "", "", printMoney(p.Pending)},
			Bold:  true,
		})
		for _, b := range p.Bills {
			rows = append(rows, printing.Row{
				Cells: []string{"", "", b.RefName, printISODate(b.DueDate),
					// Days overdue is a COUNT, not money: strconv, and never
					// FormatMoney, which would print "45.00 days".
					strconv.Itoa(b.OverdueDays), b.SlabLabel, printMoney(b.Pending)},
				Indent: 1,
			})
		}
	}

	var footer []printing.Row
	for _, st := range out.SlabTotals {
		footer = append(footer, printing.Row{
			Cells: []string{"Ageing " + st.Label, "", "", "", "", "", printMoney(st.Amount)},
		})
	}
	footer = append(footer, printing.Row{
		Cells:     []string{"Total outstanding", "", "", "", "", "", printMoney(out.Total)},
		Bold:      true,
		RuleAbove: true,
	})

	title := "Outstanding Receivables"
	if kind == reporting.Payables {
		title = "Outstanding Payables"
	}
	return printing.ReportTable{
		Title:     title,
		Subtitle:  "Bill-wise, aged " + printAsOnSubtitle(out.AsOn),
		Landscape: true,
		Columns: []printing.Column{
			{Header: "Party", Align: printing.AlignLeft, Width: 4},
			{Header: "Group", Align: printing.AlignLeft, Width: 3},
			{Header: "Bill Ref", Align: printing.AlignLeft, Width: 3},
			{Header: "Due Date", Align: printing.AlignLeft, Width: 2},
			{Header: "Days Overdue", Align: printing.AlignRight, Width: 1.6},
			{Header: "Ageing", Align: printing.AlignLeft, Width: 2},
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
	for _, it := range sum.Items {
		rows = append(rows, printing.Row{
			Cells: []string{it.ItemName, it.UnitSymbol,
				printing.FormatQty(it.OpeningQty), printMoneyOrBlank(it.OpeningValue),
				printing.FormatQty(it.InwardQty), printMoneyOrBlank(it.InwardValue),
				printing.FormatQty(it.OutwardQty), printMoneyOrBlank(it.OutwardValue),
				printing.FormatQty(it.ClosingQty), printMoney(it.ClosingValue)},
			Bold: true,
		})
		for _, p := range it.Positions {
			bases[p.ValuationMethod] = true
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

	footer := []printing.Row{{
		Cells: []string{"Closing value of the whole inventory", "", "", "", "", "", "", "", "",
			printMoney(sum.TotalValue)},
		Bold:      true,
		RuleAbove: true,
	}}
	// A statement must be able to state its own basis. Nothing on this report is
	// stored — every figure is a replay of the movement ledger through a valuer —
	// so a reader who cannot see WHICH valuer ran cannot check any of it.
	if b := printSortedKeys(bases); b != "" {
		footer = append(footer, printLabelRow("Valuation basis: "+b+
			". Value is never stored; every figure above is a replay of the movement ledger."))
	}

	return printing.ReportTable{
		Title:     "Stock Summary",
		Subtitle:  printAsOnSubtitle(sum.AsOn),
		Landscape: true,
		Columns: []printing.Column{
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
		},
		Rows:   rows,
		Footer: footer,
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
	footer = append(footer,
		printLabelRow("Social-security ruleset: "+reg.SSRuleSetVersion+
			"   Income-tax ruleset: "+reg.ITRuleSetVersion),
		printLabelRow("The employer's contributions are NOT part of gross pay and are "+
			"not included in it: they are a separate cost to the company."))

	return printing.ReportTable{
		Title:     "Payroll Register",
		Subtitle:  "Wage month " + reg.WageMonth + " — pay run " + reg.Status,
		Landscape: true,
		Columns: []printing.Column{
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
		},
		Rows:   rows,
		Footer: footer,
	}, true
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
