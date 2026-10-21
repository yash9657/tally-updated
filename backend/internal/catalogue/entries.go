package catalogue

import "github.com/khata/cored/internal/query"

// entries.go is DATA. Every string here is read by a model or matched by the
// selector; none of it is read by the PDP or the compiler as authority.
//
// HOW TO ADD A REPORT: add it to authz.KnownReportNames (with the ACL
// migration D95 demands), then add an Entry here — completeness_test.go fails
// until you do. Write the Description's "does NOT answer" half first; it is
// the half that stops a wrong-but-plausible answer.

// Small constructors so the example DSL reads like the question it answers.
func m(fn query.AggFunc, col, as string) query.Metric {
	return query.Metric{Func: fn, Column: col, As: as}
}
func f(col string, op query.Op, vals ...string) query.Filter {
	return query.Filter{Column: col, Op: op, Values: vals}
}
func desc(col string) []query.OrderTerm { return []query.OrderTerm{{Column: col, Desc: true}} }

const (
	noDSLPayroll = "Payroll figures live in pay_run/payslip tables no DSL entity reads, deliberately: " +
		"salaries and PAN/UAN/IP numbers must not be reachable through the general query surface " +
		"(migration 0035 disallows these reports by default). Call the report route."
	noDSLStatutoryFile = "A statutory return is a prescribed document assembled by its own package " +
		"with its own disclosures; a DSL sum would drop the disclosures. Call the report route."
)

var reportEntries = []Entry{
	{
		Report: "trial-balance", Title: "Trial Balance",
		Description: "Closing balance of every ledger, grouped, as at a date; debits equal credits for a balanced book. " +
			"Includes opening balances. Does NOT show period movement alone (use Day Book / postings) or bill-wise detail.",
		Synonyms:    []string{"trial balance", "TB", "ledger balances", "all account balances", "closing balances"},
		Hinglish:    []string{"kaccha chittha", "saare khaton ka balance", "khata balance", "tally hua ya nahi"},
		Questions:   []string{"show me the trial balance", "saare khaton ka balance dikhao", "is my trial balance tallying?"},
		DSL:         []query.Query{{Entity: query.EntityLedgerBalances, Dimensions: []string{"group_name", "ledger_name"}, Metrics: []query.Metric{m(query.AggSum, "closing_net", "closing")}}},
		DSLFidelity: "exact", ParityTest: "TestExecute_ClosingNetEqualsTrialBalance",
	},
	{
		Report: "balance-sheet", Title: "Balance Sheet",
		Description: "Assets against liabilities and capital as at a date, with closing stock valued and the year's profit carried in. " +
			"The DSL example gives ledger closings by group for ASSET/LIABILITY natures only — it omits closing stock valuation and current-period profit.",
		Synonyms:    []string{"balance sheet", "financial position", "assets and liabilities", "net worth"},
		Hinglish:    []string{"pakka chittha", "tulan patra", "sampatti aur dayitva", "company ki haisiyat"},
		Questions:   []string{"what does my balance sheet look like", "balance sheet dikhao 31 march ka", "what are our total assets"},
		DSL:         []query.Query{{Entity: query.EntityLedgerBalances, Dimensions: []string{"nature", "group_name"}, Metrics: []query.Metric{m(query.AggSum, "closing_net", "closing")}, Filters: []query.Filter{f("nature", query.OpIn, "ASSET", "LIABILITY")}}},
		DSLFidelity: "partial",
	},
	{
		Report: "profit-and-loss", Title: "Profit & Loss",
		Description: "Income against expenses for a period, rolled up to the P&L root groups (Sales, Purchase, Direct/Indirect). " +
			"Sales and purchase totals must be measured on postings natural_net, never on voucher taxable_value (D51). Stock adjustments are the report's own.",
		Synonyms:    []string{"profit and loss", "P&L", "income statement", "profit", "net profit", "total sales", "revenue", "expenses"},
		Hinglish:    []string{"nafa nuksan", "munafa", "labh hani", "kitna kamaya", "kharcha kitna hua", "bikri kitni hui"},
		Questions:   []string{"what was my profit last year", "is saal kitna munafa hua", "total sales this month", "how much did we spend on rent"},
		DSL:         []query.Query{{Entity: query.EntityPostings, Metrics: []query.Metric{m(query.AggSum, "natural_net", "sales")}, Filters: []query.Filter{f("pl_group", query.OpEq, "Sales Accounts")}}},
		DSLFidelity: "exact", ParityTest: "TestExecute_NetSalesMatchesProfitAndLoss",
	},
	{
		Report: "day-book", Title: "Day Book",
		Description: "Every current voucher in a date window, in entry order, with its postings. Answers what was entered when; it is not a balance.",
		Synonyms:    []string{"day book", "daybook", "vouchers entered", "transactions list", "journal register"},
		Hinglish:    []string{"rojnamcha", "roz ki entries", "aaj ki entry", "kal kya entry hui"},
		Questions:   []string{"show today's vouchers", "aaj kitni entry hui", "how many invoices did we raise in May"},
		DSL:         []query.Query{{Entity: query.EntityVouchers, Dimensions: []string{"voucher_type_name"}, Metrics: []query.Metric{m(query.AggCount, "*", "vouchers")}}},
		DSLFidelity: "partial",
	},
	{
		Report: "outstandings", Title: "Outstandings (receivables)",
		Description: "Bill-wise amounts customers still owe, with ageing, including opening bills. Always an as-at-today position in the DSL. " +
			"Supplier dues are the separate bills-payable report.",
		Synonyms:    []string{"outstandings", "receivables", "debtors", "who owes us", "overdue bills", "ageing", "pending payments from customers"},
		Hinglish:    []string{"baki", "udhaar", "lena baki", "paisa aana baki", "kiska paisa baki hai", "vasooli", "lenadari"},
		Questions:   []string{"who owes us money", "Kaveri ka kitna baki hai", "which customer bills are overdue by 90 days"},
		DSL:         []query.Query{{Entity: query.EntityOutstandings, Dimensions: []string{"ledger_name"}, Metrics: []query.Metric{m(query.AggSum, "pending", "pending")}, Filters: []query.Filter{f("nature", query.OpEq, "ASSET")}, OrderBy: desc("pending")}},
		DSLFidelity: "exact", ParityTest: "TestExecute_OutstandingsMatchesTheOutstandingsReport",
	},
	{
		Report: "bills-payable", Title: "Bills Payable",
		Description: "Bill-wise amounts we still owe suppliers, with ageing. The payables half of outstandings, named separately so it can be granted separately.",
		Synonyms:    []string{"bills payable", "payables", "creditors", "what we owe suppliers", "supplier dues"},
		Hinglish:    []string{"dena baki", "supplier ko kitna dena hai", "denadari", "creditor ka baki"},
		Questions:   []string{"how much do we owe suppliers", "Bharat Metals ko kitna dena hai", "which supplier bills are due this week"},
		DSL:         []query.Query{{Entity: query.EntityOutstandings, Dimensions: []string{"ledger_name"}, Metrics: []query.Metric{m(query.AggSum, "pending", "pending")}, Filters: []query.Filter{f("nature", query.OpEq, "LIABILITY")}, OrderBy: desc("pending")}},
		DSLFidelity: "exact", ParityTest: "TestExecute_OutstandingsMatchesTheOutstandingsReport",
	},
	{
		Report: "cash-flow", Title: "Cash Flow Forecast",
		Description: "Bucketed forecast of cash in and out from open bills and their due dates (internal/forecast), with its basis stated. A projection, not a statement of what moved.",
		Synonyms:    []string{"cash flow", "cash forecast", "liquidity", "when will money come in", "cash position next weeks"},
		Hinglish:    []string{"paisa kab aayega", "nakdi", "cash kitna rahega", "agle hafte ka cash"},
		Questions:   []string{"what is our cash flow forecast", "agle mahine cash kitna rahega", "will we run short of cash"},
		NoDSLReason: "A forecast buckets open bills by expected date with rules no DSL entity carries; call the report.",
	},
	{
		Report: "anomalies", Title: "Anomalies",
		Description: "Rule-based flags over the books (duplicate-looking vouchers, unusual amounts, missing references). Each flag is a lead to check, not a finding.",
		Synonyms:    []string{"anomalies", "suspicious entries", "unusual transactions", "duplicates", "errors in books"},
		Hinglish:    []string{"gadbad", "galat entry", "kuch ajeeb", "dohri entry"},
		Questions:   []string{"are there any suspicious entries", "koi galat entry to nahi", "find duplicate vouchers"},
		NoDSLReason: "Anomaly rules are computed by internal/forecast, not stored rows; call the report.",
	},
	{
		Report: "gstr1", Title: "GSTR-1",
		Description: "Outward supplies return: B2B/B2C invoices, credit/debit notes, HSN summary, as the portal expects. The DSL example is the taxable base and tax heads of Sales vouchers only; it omits notes and the portal sections.",
		Synonyms:    []string{"GSTR-1", "GSTR1", "outward supplies", "sales return filing", "B2B invoices"},
		Hinglish:    []string{"bikri ka GST", "GSTR ek", "sales ka return"},
		Questions:   []string{"prepare GSTR-1 for last month", "GSTR-1 me kitna taxable hai", "list B2B invoices for GST"},
		DSL:         []query.Query{{Entity: query.EntityVouchers, Metrics: []query.Metric{m(query.AggSum, "taxable_value", "taxable"), m(query.AggSum, "igst", "igst"), m(query.AggSum, "cgst", "cgst"), m(query.AggSum, "sgst", "sgst")}, Filters: []query.Filter{f("voucher_type_name", query.OpEq, "Sales")}}},
		DSLFidelity: "partial",
	},
	{
		Report: "gstr3b", Title: "GSTR-3B",
		Description: "Monthly summary return: output tax, eligible ITC and the net payable. ITC eligibility is the ITC engine's, not a sum of purchase tax.",
		Synonyms:    []string{"GSTR-3B", "3B", "GST payable", "monthly GST return", "tax liability"},
		Hinglish:    []string{"GST kitna bharna hai", "teen B", "GST dena"},
		Questions:   []string{"how much GST do we have to pay this month", "is mahine GST kitna bharna hai", "show GSTR-3B summary"},
		NoDSLReason: "Net payable needs eligible ITC from the reconciliation engine; a DSL sum of purchase tax would overstate ITC. Call the report.",
	},
	{
		Report: "reconciliation-queue", Title: "Reconciliation Queue",
		Description: "Imported or synced items that could not be matched to books automatically and wait for a person.",
		Synonyms:    []string{"reconciliation queue", "unmatched items", "pending matching", "import exceptions"},
		Hinglish:    []string{"match nahi hua", "pending milan", "atki hui entries"},
		Questions:   []string{"what is waiting to be reconciled", "kaun si entries match nahi hui"},
		NoDSLReason: "A work queue, not an aggregate; call the report.",
	},
	{
		Report: "bank-reconciliation", Title: "Bank Reconciliation Statement",
		Description: "Book balance vs bank balance as at a date, with cheques issued/deposited but not yet cleared. The DSL example counts imported statement lines by match status only — clearance lives in bank_reconciliation, not in statement lines.",
		Synonyms:    []string{"bank reconciliation", "BRS", "uncleared cheques", "bank vs books", "reconcile bank"},
		Hinglish:    []string{"bank milan", "bank se match", "cheque clear hua ya nahi"},
		Questions:   []string{"show the bank reconciliation statement", "kaun se cheque clear nahi hue", "how many statement lines are unmatched"},
		DSL:         []query.Query{{Entity: query.EntityBankStatementLines, Dimensions: []string{"match_status"}, Metrics: []query.Metric{m(query.AggCount, "*", "lines"), m(query.AggSum, "amount", "amount")}}},
		DSLFidelity: "partial",
	},
	{
		Report: "bank-statement", Title: "Imported Bank Statement",
		Description: "Lines of imported bank statement files for one bank ledger, as the bank reported them, with totals in and out. Deposits are money into the account.",
		Synonyms:    []string{"bank statement", "passbook", "bank transactions", "deposits", "withdrawals"},
		Hinglish:    []string{"bank ka statement", "passbook entry", "khate me kitna aaya", "bank se kitna gaya"},
		Questions:   []string{"total deposits in HDFC last month", "bank me kitna paisa aaya", "show withdrawals by month"},
		DSL:         []query.Query{{Entity: query.EntityBankStatementLines, Dimensions: []string{"bank_ledger_name", "month"}, Metrics: []query.Metric{m(query.AggSum, "deposits", "deposits"), m(query.AggSum, "withdrawals", "withdrawals")}}},
		DSLFidelity: "exact", ParityTest: "TestExecute_BankStatementLinesMatchTheImportedStatement",
	},
	{
		Report: "tds-register", Title: "TDS Register",
		Description: "TDS deducted per deductee and section for a period, with challan allocation state. Contains PANs.",
		Synonyms:    []string{"TDS register", "tax deducted at source", "TDS deducted", "194C 194J deductions"},
		Hinglish:    []string{"TDS kitna kata", "TDS ka hisaab"},
		Questions:   []string{"how much TDS did we deduct this quarter", "TDS kitna kata is mahine"},
		NoDSLReason: "TDS lives in tds_deduction with PANs no DSL entity exposes; call the report.",
		Sensitive:   true,
	},
	{
		Report: "cost-centre-summary", Title: "Cost Centre Summary",
		Description: "Postings allocated to cost centres, as a tree, with the unallocated remainder stated.",
		Synonyms:    []string{"cost centre summary", "cost center", "department wise expense", "project wise cost"},
		Hinglish:    []string{"vibhag ka kharcha", "project wise kharcha"},
		Questions:   []string{"expenses by cost centre", "project wise kharcha dikhao"},
		NoDSLReason: "No cost-centre allocation entity in the DSL yet (owed, PLAN-AGENTIC W3); call the report.",
	},
	{
		Report: "cost-category-summary", Title: "Cost Category Summary",
		Description: "Allocations per cost category (axis), with how much of each posting was allocated.",
		Synonyms:    []string{"cost category summary", "cost category", "allocation by category"},
		Hinglish:    []string{"category wise kharcha"},
		Questions:   []string{"show cost category summary", "category wise kharcha kitna hai"},
		NoDSLReason: "No cost-centre allocation entity in the DSL yet (owed); call the report.",
	},
	{
		Report: "sales-order-book", Title: "Sales Order Book",
		Description: "Open and fulfilled customer orders with pending quantity and value, and which are overdue.",
		Synonyms:    []string{"sales order book", "sales orders", "pending orders", "orders to dispatch"},
		Hinglish:    []string{"order pending", "maal bhejna baki", "customer ke order"},
		Questions:   []string{"which sales orders are pending", "kitne order bhejna baki hai"},
		NoDSLReason: "No orders entity in the DSL yet (owed); call the report.",
	},
	{
		Report: "purchase-order-book", Title: "Purchase Order Book",
		Description: "Open and received purchase orders with pending quantity and value.",
		Synonyms:    []string{"purchase order book", "purchase orders", "orders placed with suppliers"},
		Hinglish:    []string{"maal aana baki", "supplier ko order diya"},
		Questions:   []string{"which purchase orders are still open", "kaunsa maal aana baki hai"},
		NoDSLReason: "No orders entity in the DSL yet (owed); call the report.",
	},
	{
		Report: "stock-summary", Title: "Stock Summary",
		Description: "Quantity and VALUE on hand per item/godown/batch as at a date, from openings plus every movement replayed through the item's valuer. " +
			"The only surface that knows stock value; stock_movements cannot compute it.",
		Synonyms:    []string{"stock summary", "inventory", "stock on hand", "closing stock", "stock value"},
		Hinglish:    []string{"stock kitna hai", "maal kitna bacha", "godam me kya hai", "maal ki keemat"},
		Questions:   []string{"how much stock do we have", "Widget ka stock kitna bacha hai", "what is our closing stock value"},
		NoDSLReason: "Stock value needs the valuer replay (weighted average/FIFO/LIFO) and openings; SQL cannot replay a valuer. Call the report.",
	},
	{
		Report: "stock-movement", Title: "Stock Movement",
		Description: "Inward and outward quantity (and valued amounts) per item for a period. The DSL example is exact on quantities; outward VALUE is the valuer's and only the report has it.",
		Synonyms:    []string{"stock movement", "stock in and out", "inward outward", "item movement", "goods received and issued"},
		Hinglish:    []string{"maal aaya gaya", "aavak javak", "kitna maal bika", "kitna maal aaya"},
		Questions:   []string{"how many units of Widget did we sell last month", "is mahine kitna maal aaya", "stock movement by item"},
		DSL:         []query.Query{{Entity: query.EntityStockMovements, Dimensions: []string{"item_name"}, Metrics: []query.Metric{m(query.AggSum, "in_qty", "in_qty"), m(query.AggSum, "out_qty", "out_qty"), m(query.AggSum, "net_qty", "net_qty")}}},
		DSLFidelity: "exact", ParityTest: "TestExecute_StockMovementsMatchTheStockMovementReport",
	},
	{
		Report: "godown-summary", Title: "Godown Summary",
		Description: "Stock per godown (location) as at a date, valued. The DSL example gives net movement quantity per godown and omits openings.",
		Synonyms:    []string{"godown summary", "location wise stock", "warehouse stock", "godown"},
		Hinglish:    []string{"godam wise stock", "kis godam me kitna maal"},
		Questions:   []string{"stock by warehouse", "kis godam me kitna maal hai"},
		DSL:         []query.Query{{Entity: query.EntityStockMovements, Dimensions: []string{"godown_name", "item_name"}, Metrics: []query.Metric{m(query.AggSum, "net_qty", "net_qty")}}},
		DSLFidelity: "partial",
	},
	{
		Report: "batch-summary", Title: "Batch Summary",
		Description: "Stock per batch with manufacturing and expiry dates and expiry bands. The DSL example is net movement per batch only (no expiry).",
		Synonyms:    []string{"batch summary", "batch wise stock", "expiry report", "near expiry"},
		Hinglish:    []string{"batch wise maal", "kab expire hoga", "expiry wala maal"},
		Questions:   []string{"which batches expire this month", "expiry wala maal dikhao"},
		DSL:         []query.Query{{Entity: query.EntityStockMovements, Dimensions: []string{"item_name", "batch_name"}, Metrics: []query.Metric{m(query.AggSum, "net_qty", "net_qty")}}},
		DSLFidelity: "partial",
	},
	{
		Report: "stock-item-vouchers", Title: "Stock Item Vouchers",
		Description: "Every voucher that moved one item, with quantity, rate and running balance.",
		Synonyms:    []string{"stock item vouchers", "item ledger", "item transactions", "item history"},
		Hinglish:    []string{"item ki entries", "item ka hisaab"},
		Questions:   []string{"show all vouchers for Widget", "Gadget ka hisaab dikhao", "at what rate did we buy Widget"},
		DSL:         []query.Query{{Entity: query.EntityStockMovements, Dimensions: []string{"voucher_type_name", "party_name"}, Metrics: []query.Metric{m(query.AggSum, "qty", "qty"), m(query.AggAvg, "rate", "avg_rate")}, Filters: []query.Filter{f("item_name", query.OpEq, "Widget")}}},
		DSLFidelity: "partial",
	},
	{
		Report: "itc-reconciliation", Title: "ITC Reconciliation (GSTR-2B)",
		Description: "Purchase register matched against GSTR-2B / supplier filings: matched, mismatched, missing in 2B, missing in books, with ITC effect.",
		Synonyms:    []string{"ITC reconciliation", "2B reconciliation", "input tax credit match", "GSTR-2B"},
		Hinglish:    []string{"ITC milan", "2B se match", "input credit kitna milega"},
		Questions:   []string{"which supplier invoices are missing in 2B", "ITC kitna milega is mahine"},
		NoDSLReason: "Matching state is the ITC engine's; call the report.",
	},
	{
		Report: "ims-inbox", Title: "IMS Inbox",
		Description: "Invoice Management System worklist: supplier documents to accept, reject or keep pending, with what was done to each.",
		Synonyms:    []string{"IMS inbox", "invoice management system", "accept reject invoices"},
		Hinglish:    []string{"IMS me kya pending hai", "invoice accept karna"},
		Questions:   []string{"what is pending in IMS", "IMS me kitne invoice pending hain"},
		NoDSLReason: "A worklist over gst_inward_document; call the report.",
	},
	{
		Report: "payroll-register", Title: "Payroll Register",
		Description: "Every employee's gross, deductions and net for a pay run.",
		Synonyms:    []string{"payroll register", "salary register", "wage register", "salary sheet"},
		Hinglish:    []string{"tankhwah register", "salary kitni gayi"},
		Questions:   []string{"show this month's salary register", "is mahine salary kitni gayi"},
		NoDSLReason: noDSLPayroll, Sensitive: true,
	},
	{
		Report: "payroll-payslip", Title: "Payslip",
		Description: "One employee's payslip in full, including every component.",
		Synonyms:    []string{"payslip", "salary slip", "pay slip"},
		Hinglish:    []string{"salary slip", "vetan parchi"},
		Questions:   []string{"show Ramesh's payslip for June", "salary slip nikalo"},
		NoDSLReason: noDSLPayroll, Sensitive: true,
	},
	{
		Report: "payroll-form-138", Title: "Form 138 (TDS on salary)",
		Description: "Quarterly statement of salary and TDS per employee with PAN (the Income-tax Act 2025 successor to Form 24Q).",
		Synonyms:    []string{"form 138", "24Q", "TDS on salary return", "salary TDS statement"},
		Hinglish:    []string{"salary TDS ka return"},
		Questions:   []string{"prepare form 138 for Q1", "salary TDS ka return banao"},
		NoDSLReason: noDSLStatutoryFile, Sensitive: true,
	},
	{
		Report: "payroll-pf-ecr", Title: "PF ECR",
		Description: "EPFO electronic challan-cum-return: UAN and PF wages per employee per month.",
		Synonyms:    []string{"PF ECR", "provident fund return", "EPF challan"},
		Hinglish:    []string{"PF ka challan", "PF return"},
		Questions:   []string{"generate the PF ECR for this month", "PF ka challan banao"},
		NoDSLReason: noDSLStatutoryFile, Sensitive: true,
	},
	{
		Report: "payroll-esi-return", Title: "ESI Return",
		Description: "ESIC monthly contribution return: IP number, wages and days per employee.",
		Synonyms:    []string{"ESI return", "ESIC contribution", "ESI challan"},
		Hinglish:    []string{"ESI ka return"},
		Questions:   []string{"prepare the ESI return", "ESI ka return banao"},
		NoDSLReason: noDSLStatutoryFile, Sensitive: true,
	},
	{
		Report: "budget-variance", Title: "Budget Variance",
		Description: "Budget against actual per group, ledger or cost centre for the budget's period, with over/under-spend.",
		Synonyms:    []string{"budget variance", "budget vs actual", "over budget", "budget report"},
		Hinglish:    []string{"budget se zyada kharcha", "budget ke mukable"},
		Questions:   []string{"where are we over budget", "budget se zyada kharcha kahan hua"},
		NoDSLReason: "Budget lines are not a DSL entity; call the report.",
	},
	{
		Report: "gstr9", Title: "GSTR-9 Annual Return",
		Description: "The year's returns reconciled to the books, stating where they disagree. Not filing-ready without portal-schema verification.",
		Synonyms:    []string{"GSTR-9", "GSTR9", "annual GST return", "GST annual"},
		Hinglish:    []string{"saal ka GST return", "GST annual return"},
		Questions:   []string{"prepare the annual GST return", "saal ka GST return dikhao"},
		NoDSLReason: noDSLStatutoryFile,
	},
	{
		Report: "unadjusted-forex", Title: "Unadjusted Forex Gain/Loss",
		Description: "Unrealised exchange difference on open foreign-currency bills at a closing rate, not yet posted.",
		Synonyms:    []string{"unadjusted forex", "forex gain loss", "exchange difference", "foreign currency revaluation"},
		Hinglish:    []string{"videshi mudra ka nafa nuksan", "dollar rate ka fark"},
		Questions:   []string{"what is our unrealised forex gain", "dollar rate ka fark kitna hai"},
		NoDSLReason: "Needs a closing rate per currency; call the report.",
	},
}

var entityEntries = []EntityEntry{
	{Entity: query.EntityBillAllocations, Description: "Current voucher bill allocations: trace invoices, partial receipts and credits by reference and voucher date. Excludes opening bills; debit_minus_credit is signed movement, not always payable.", Synonyms: []string{"bill allocations", "partial receipts", "credit allocation"}, Hinglish: []string{"aadha bhugtan", "bill ka adjustment", "बिल आवंटन"}},
	{Entity: query.EntityLedgerBalances, Description: "Per-ledger balances (movement, opening, closing_net) by ledger/group/nature. No date range.",
		Synonyms: []string{"ledger balance", "account balance", "group total"}, Hinglish: []string{"khata balance"}},
	{Entity: query.EntityVouchers, Description: "Current voucher versions: type, class, party, GST base and heads, by month/quarter/year.",
		Synonyms: []string{"vouchers", "invoices", "entries", "bills raised"}, Hinglish: []string{"entry", "bill"}},
	{Entity: query.EntityPostings, Description: "Posting lines of current vouchers; natural_net summed with pl_group is the P&L figure.",
		Synonyms: []string{"postings", "sales total", "purchase total", "expense total"}, Hinglish: []string{"bikri", "kharcha", "kamai"}},
	{Entity: query.EntityOutstandings, Description: "Bill-wise pending as at today, receivable (ASSET) or payable (LIABILITY), incl. opening bills.",
		Synonyms: []string{"outstanding", "receivable", "payable", "due"}, Hinglish: []string{"baki", "udhaar", "dena", "lena"}},
	{Entity: query.EntityStockMovements, Description: "Inventory movement lines: in/out/net qty, amount AS ENTERED, rate; never valued cost.",
		Synonyms: []string{"stock movement", "units sold", "units bought", "purchase rate"}, Hinglish: []string{"maal", "aavak", "javak"}},
	{Entity: query.EntityBankStatementLines, Description: "Imported bank statement lines: deposits, withdrawals, match status; narration is filter-only.",
		Synonyms: []string{"bank statement", "deposit", "withdrawal"}, Hinglish: []string{"bank me aaya", "bank se gaya"}},
	{Entity: query.EntityProfileFacts, Description: "Current deterministic facts about parties/items/streams/the tenant (DSO, payment lag, ABC class, run-rate).",
		Synonyms: []string{"profile", "DSO", "payment behaviour", "ABC class", "run rate", "recurring payment"}, Hinglish: []string{"kitne din me paisa deta hai", "har mahine ka kharcha"}},
}
