// expected.go holds the PINNED four-statement reconciliation oracle document
// (ExpectedReconciliation) and the ReconcileFull harness that drives it.
//
// The pinned ExpectedReconciliation struct family is the verbatim contract the
// migration corpus author and the importer/reporting implementer agree on. JSON
// lives in corpora/migration/<id>.expected.json. All money fields are
// NUMERIC(19,4) decimal STRINGS rendered StringFixed(4) (paisa precision); rates
// are integer-percent strings ("18"); state codes are 2-char strings ("29").
// LoadExpectedReconciliation keeps DisallowUnknownFields, so every key is exact.
//
// ReconcileFull layers Balance Sheet, P&L, GSTR-1, GSTR-3B and Outstandings
// checks on top
// of the existing Trial Balance gate (Reconcile). It never early-returns on a
// single mismatch: it accumulates ALL discrepancies, sorts them, and joins them
// into one multi-line error so a migration's full divergence is visible in a
// single run. Each problem string names the statement + ledger/bucket so a CA
// (or CI log) can locate it instantly.
package migration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/compliance/gstr"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/reporting"
)

// --------------------------------------------------------------------------
// PINNED ExpectedReconciliation struct family — DO NOT change field names/tags
// without coordinating with the corpus author AND the importer implementer.
// --------------------------------------------------------------------------

// ExpectedReconciliation is the four-statement oracle document for one corpus.
type ExpectedReconciliation struct {
	ID              string `json:"id"`
	Description     string `json:"description"`
	TallyExportFile string `json:"tallyExportFile"`
	// CreatedBy/import knobs the harness needs.
	GSTIN         string                  `json:"gstin,omitempty"`      // filing company GSTIN (GSTR-1 top-level)
	StateCode     string                  `json:"stateCode,omitempty"`  // company home state (WS3 default)
	Period        *Period                 `json:"period,omitempty"`     // GSTR-1/3B [from,to]; nil = skip both
	TaxLedgers    *TaxLedgerNames         `json:"taxLedgers,omitempty"` // OUTPUT-tax names resolved to IDs for gstr Options + posting recon
	ITCLedgers    *TaxLedgerNames         `json:"itcLedgers,omitempty"` // INPUT-tax names for the GSTR-3B table-4 posting recon
	TrialBalance  ExpectedTrialBalanceDoc `json:"trialBalance"`         // REUSES existing structs verbatim
	BalanceSheet  *ExpectedBalanceSheet   `json:"balanceSheet,omitempty"`
	ProfitAndLoss *ExpectedPL             `json:"profitAndLoss,omitempty"`
	GSTR1         *ExpectedGSTR1          `json:"gstr1,omitempty"`
	GSTR3B        *ExpectedGSTR3B         `json:"gstr3b,omitempty"`
	Outstandings  *ExpectedOutstandings   `json:"outstandings,omitempty"`
}

// Period is an inclusive ISO date range [From, To] (YYYY-MM-DD).
type Period struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// TaxLedgerNames names the three output-tax ledgers the harness resolves to IDs.
type TaxLedgerNames struct {
	CGST string `json:"cgst"`
	SGST string `json:"sgst"`
	IGST string `json:"igst"`
}

// ExpectedTrialBalanceDoc reuses the EXISTING wire shape so Reconcile() is
// untouched: AsTrialBalance() adapts it back to ExpectedTrialBalance.
type ExpectedTrialBalanceDoc struct {
	Rows   []ExpectedBalance `json:"rows"`   // existing ExpectedBalance{ledger,debit,credit}
	Totals ExpectedTotals    `json:"totals"` // existing ExpectedTotals{debit,credit,balanced}
}

// ExpectedBalanceSheet is the hand-derived Balance Sheet oracle.
type ExpectedBalanceSheet struct {
	AssetsTotal      string `json:"assetsTotal"`
	LiabilitiesTotal string `json:"liabilitiesTotal"` // includes folded net profit
	NetProfit        string `json:"netProfit"`
	Balanced         bool   `json:"balanced"`
}

// ExpectedPL is the hand-derived Profit & Loss oracle.
type ExpectedPL struct {
	IncomeTotal  string `json:"incomeTotal"`
	ExpenseTotal string `json:"expenseTotal"`
	NetProfit    string `json:"netProfit"`
}

// ExpectedGSTR1 mirrors gstr.GSTR1 rollups at the level we assert (totals +
// per-rate HSN + B2CS buckets + B2B per-ctin). NOT full invoice-wise.
type ExpectedGSTR1 struct {
	FP        string             `json:"fp"`             // "MMYYYY"
	TaxTotals ExpectedTaxTotals  `json:"taxTotals"`      // summed across sections == output-tax postings
	HSN       []ExpectedHSNRow   `json:"hsn"`            // per (hsn,rate); hsn "" allowed
	B2CS      []ExpectedB2CS     `json:"b2cs,omitempty"` // per (splyTy,pos,rate)
	B2B       []ExpectedB2BParty `json:"b2b,omitempty"`  // per ctin: taxable+tax rollup only
	Balanced  bool               `json:"balanced"`       // gstr.Reconciliation.Balanced()
}

// ExpectedTaxTotals is the per-head GSTR-1 tax rollup oracle.
type ExpectedTaxTotals struct {
	Taxable string `json:"taxable"`
	CGST    string `json:"cgst"`
	SGST    string `json:"sgst"`
	IGST    string `json:"igst"`
}

// ExpectedHSNRow is one (hsn,rate) HSN-summary oracle row.
type ExpectedHSNRow struct {
	HSN     string `json:"hsn"`
	Rate    string `json:"rate"`
	Taxable string `json:"taxable"`
	CGST    string `json:"cgst"`
	SGST    string `json:"sgst"`
	IGST    string `json:"igst"`
}

// ExpectedB2CS is one (splyTy,pos,rate) B2C-Small oracle bucket.
type ExpectedB2CS struct {
	SplyTy  string `json:"splyTy"`
	Pos     string `json:"pos"`
	Rate    string `json:"rate"`
	Taxable string `json:"taxable"`
	CGST    string `json:"cgst"`
	SGST    string `json:"sgst"`
	IGST    string `json:"igst"`
}

// ExpectedB2BParty is one counter-party's B2B taxable+tax rollup oracle.
type ExpectedB2BParty struct {
	CTIN    string `json:"ctin"`
	Taxable string `json:"taxable"`
	CGST    string `json:"cgst"`
	SGST    string `json:"sgst"`
	IGST    string `json:"igst"`
}

// ExpectedGSTR3B mirrors gstr.GSTR3B at the level we assert: the 3.1(a) outward
// row, the 3.1(d) reverse-charge inward row, the 3.2 inter-state-to-unregistered
// rows, and the table-4C net ITC.
//
// LIMITATION OF THE MIGRATION-CORPUS ORACLE (structural, not an oversight): the
// Tally importer builds GSTParams ONLY for Sales vouchers (migration.go:514,
// :566), so no imported corpus voucher can stamp the PURCHASE-side GST columns.
// RCMInward, ITC, ITCReversed and ITCIneligible are therefore necessarily
// all-zero for every corpus, and no amount of new fixtures can change that —
// the ITC half of table 4 is unreachable from a Tally XML import.
//
// The NON-ZERO coverage lives in two places, neither of which is this one:
//   - backend/internal/compliance/gstr/gstr3b_integration_test.go posts
//     purchases directly through ledger.Service (RCM, reversals, ineligible
//     credit, the version guard).
//   - corpora/gstr2b/ carries the GSTR-2A/2B matching cases, re-derived
//     independently by corpora/validate.py.
type ExpectedGSTR3B struct {
	FP string `json:"fp"` // "MMYYYY"
	// Outward is table 3.1(a): taxable + the three tax heads.
	Outward ExpectedTaxTotals `json:"outward"`
	// RCMInward is table 3.1(d): inward supplies liable to reverse charge.
	RCMInward ExpectedTaxTotals `json:"rcmInward"`
	// InterUnreg is table 3.2: inter-state supplies to unregistered persons, by POS.
	InterUnreg []ExpectedInterSup `json:"interUnreg,omitempty"`
	// ITC is table 4(C): NET ITC available, i.e. sum(4A) - sum(4B).
	ITC ExpectedTaxTotals `json:"itc"`
	// ITCReversed is table 4(B) and ITCIneligible is table 4(D). Both are
	// OPTIONAL — a fixture that omits them asserts nothing about them, which is
	// what every migration corpus does today because none can reach the ITC
	// half at all. They are pointers rather than values precisely so that
	// "absent" and "asserted to be zero" stay different claims: a value field
	// would silently assert zero for every existing fixture and the assertion
	// would be vacuous rather than deliberate.
	ITCReversed   *ExpectedTaxTotals `json:"itcReversed,omitempty"`
	ITCIneligible *ExpectedTaxTotals `json:"itcIneligible,omitempty"`
	// Balanced is gstr.Reconciliation3B.Balanced() — the outward tie plus the
	// two ITC invariants: sum(4A) + sum(4D) == input-tax postings, and
	// sum(4A) - sum(4B) == 4C.
	Balanced bool `json:"balanced"`
}

// ExpectedInterSup is one place-of-supply row of the 3B table-3.2 oracle. Only
// IGST can arise on an inter-state supply, so there is no CGST/SGST here.
type ExpectedInterSup struct {
	Pos     string `json:"pos"`
	Taxable string `json:"taxable"`
	IGST    string `json:"igst"`
}

// ExpectedOutstandings is the bill-wise receivables/payables oracle.
type ExpectedOutstandings struct {
	Receivables []ExpectedParty `json:"receivables,omitempty"`
	Payables    []ExpectedParty `json:"payables,omitempty"`
	AsOn        string          `json:"asOn,omitempty"`
}

// ExpectedParty is one party's pending position, optionally with per-bill detail.
type ExpectedParty struct {
	Ledger  string         `json:"ledger"`
	Pending string         `json:"pending"`
	Bills   []ExpectedBill `json:"bills,omitempty"`
}

// ExpectedBill is one outstanding reference's pending amount and due date.
type ExpectedBill struct {
	Ref     string `json:"ref"`
	Pending string `json:"pending"`
	DueDate string `json:"dueDate,omitempty"`
}

// --------------------------------------------------------------------------
// Loading + backward-compat shim
// --------------------------------------------------------------------------

// LoadExpectedReconciliation parses a four-statement migration oracle document.
// It keeps DisallowUnknownFields so a typo'd key fails loudly rather than being
// silently ignored.
func LoadExpectedReconciliation(r io.Reader) (ExpectedReconciliation, error) {
	var e ExpectedReconciliation
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&e); err != nil {
		return ExpectedReconciliation{}, fmt.Errorf("migration: decode expected reconciliation: %w", err)
	}
	return e, nil
}

// LoadExpectedReconciliationFile parses a four-statement oracle JSON from a path.
func LoadExpectedReconciliationFile(path string) (ExpectedReconciliation, error) {
	f, err := os.Open(path)
	if err != nil {
		return ExpectedReconciliation{}, fmt.Errorf("migration: open %s: %w", path, err)
	}
	defer f.Close()
	return LoadExpectedReconciliation(f)
}

// AsTrialBalance adapts the new four-statement document's trialBalance block back
// to the original ExpectedTrialBalance wire shape, so the existing Reconcile (the
// proven TB gate) stays callable unchanged.
func (e ExpectedReconciliation) AsTrialBalance() ExpectedTrialBalance {
	return ExpectedTrialBalance{
		ID:                   e.ID,
		Description:          e.Description,
		TallyExportFile:      e.TallyExportFile,
		ExpectedTrialBalance: e.TrialBalance.Rows,
		ExpectedTotals:       e.TrialBalance.Totals,
	}
}

// --------------------------------------------------------------------------
// ReconcileFull
// --------------------------------------------------------------------------

// LedgerResolver is the slice of the masters API ReconcileFull needs to turn the
// corpus's tax-ledger NAMES into IDs for the GSTR-1 options + posting recon.
// *masters.Service satisfies it.
type LedgerResolver interface {
	LedgerID(ctx context.Context, name string) (uuid.UUID, error)
}

// ReconcileFull runs the full four-statement reconciliation for the company in
// ctx against the hand-derived oracle exp. It delegates the Trial Balance block
// to the existing Reconcile (reuse, not reimplement), then layers Balance Sheet,
// P&L, GSTR-1, GSTR-3B and Outstandings checks, accumulating EVERY discrepancy
// before returning a single sorted, multi-line error. It returns nil only when
// no statement diverges to the paisa.
//
// res may be nil when no GSTR-1/3B block is asserted; it is only consulted to
// resolve tax-ledger names for those steps.
//
// GSTR-3B COVERAGE CAVEAT: the Tally importer attaches GSTParams only to Sales
// vouchers (migration.go:514, :566), so an imported corpus can never stamp the
// PURCHASE-side GST columns. The 3B step here therefore exercises the OUTWARD
// half (3.1(a), 3.2) with real values; 3.1(d) and table 4 are necessarily zero
// for every corpus. Non-zero ITC/reverse-charge coverage lives in the hermetic
// backend/internal/compliance/gstr/gstr3b_integration_test.go and, for the
// GSTR-2A/2B matching that decides those figures, in corpora/gstr2b/.
func ReconcileFull(ctx context.Context, rep *reporting.Service, gstrSvc *gstr.Service,
	res LedgerResolver, companyID uuid.UUID, exp ExpectedReconciliation) error {

	var problems []string

	// Step 1: Trial Balance — delegate to the existing, proven gate.
	if err := Reconcile(ctx, rep, companyID, exp.AsTrialBalance()); err != nil {
		problems = append(problems, "TRIAL BALANCE: "+err.Error())
	}

	// Step 2 + 3: Balance Sheet and P&L (with the cross-check).
	var (
		bs          reporting.BalanceSheet
		haveBS      bool
		bsNetProfit decimal.Decimal
	)
	if exp.BalanceSheet != nil {
		got, err := rep.BalanceSheet(ctx, companyID, "")
		if err != nil {
			problems = append(problems, "BALANCE SHEET: "+err.Error())
		} else {
			bs, haveBS = got, true
			bsNetProfit = got.NetProfit
			cmpDecimal(&problems, "BALANCE SHEET assetsTotal", got.Assets.Total, exp.BalanceSheet.AssetsTotal)
			cmpDecimal(&problems, "BALANCE SHEET liabilitiesTotal", got.Liabilities.Total, exp.BalanceSheet.LiabilitiesTotal)
			cmpDecimal(&problems, "BALANCE SHEET netProfit", got.NetProfit, exp.BalanceSheet.NetProfit)
			if got.IsBalanced() != exp.BalanceSheet.Balanced {
				problems = append(problems, fmt.Sprintf(
					"BALANCE SHEET balanced: got %v expected %v", got.IsBalanced(), exp.BalanceSheet.Balanced))
			}
		}
	}

	if exp.ProfitAndLoss != nil {
		pl, err := rep.ProfitAndLoss(ctx, companyID, "", "")
		if err != nil {
			problems = append(problems, "PROFIT AND LOSS: "+err.Error())
		} else {
			cmpDecimal(&problems, "PROFIT AND LOSS incomeTotal", pl.Income.Total, exp.ProfitAndLoss.IncomeTotal)
			cmpDecimal(&problems, "PROFIT AND LOSS expenseTotal", pl.Expenses.Total, exp.ProfitAndLoss.ExpenseTotal)
			cmpDecimal(&problems, "PROFIT AND LOSS netProfit", pl.NetProfit, exp.ProfitAndLoss.NetProfit)
			// Cross-check: the SAME net profit must appear on both statements. A
			// divergence catches a whole class of importer sign bugs for free.
			if haveBS && !pl.NetProfit.Equal(bsNetProfit) {
				problems = append(problems, fmt.Sprintf(
					"CONSISTENCY net-profit BS!=PL: BS %s != PL %s",
					bsNetProfit.StringFixed(4), pl.NetProfit.StringFixed(4)))
			}
		}
	}
	_ = bs // bs is retained for future drill-down assertions; netProfit captured above.

	// Step 4: GSTR-1.
	if exp.Period != nil && exp.GSTR1 != nil {
		problems = append(problems, reconcileGSTR1(ctx, gstrSvc, res, exp)...)
	}

	// Step 4b: GSTR-3B (same period block as GSTR-1).
	if exp.Period != nil && exp.GSTR3B != nil {
		problems = append(problems, reconcileGSTR3B(ctx, gstrSvc, res, exp)...)
	}

	// Step 5: Outstandings.
	if exp.Outstandings != nil {
		problems = append(problems, reconcileOutstandings(ctx, rep, exp)...)
	}

	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("migration: FULL reconciliation FAILED (%d discrepancies):\n  - %s",
			len(problems), strings.Join(problems, "\n  - "))
	}
	return nil
}

// reconcileGSTR1 resolves the tax-ledger names, runs gstr.Reconcile for the
// period, and diffs the rollups (TaxTotals, FP, HSN, B2CS, B2B) against exp.
func reconcileGSTR1(ctx context.Context, gstrSvc *gstr.Service, res LedgerResolver,
	exp ExpectedReconciliation) []string {

	var problems []string
	if gstrSvc == nil {
		return []string{"GSTR-1: no gstr service supplied but a gstr1 oracle is present"}
	}
	if exp.TaxLedgers == nil {
		return []string{"GSTR-1: gstr1 oracle present but taxLedgers (cgst/sgst/igst names) missing"}
	}

	var opts gstr.Options
	opts.GSTIN = exp.GSTIN
	resolve := func(name string) uuid.UUID {
		if name == "" || res == nil {
			return uuid.Nil
		}
		id, err := res.LedgerID(ctx, name)
		if err != nil {
			problems = append(problems, fmt.Sprintf("GSTR-1 taxLedger %q: %v", name, err))
			return uuid.Nil
		}
		return id
	}
	opts.CGSTLedgerID = resolve(exp.TaxLedgers.CGST)
	opts.SGSTLedgerID = resolve(exp.TaxLedgers.SGST)
	opts.IGSTLedgerID = resolve(exp.TaxLedgers.IGST)

	g, recon, err := gstrSvc.Reconcile(ctx, exp.Period.From, exp.Period.To, opts)
	if err != nil {
		return append(problems, "GSTR-1: "+err.Error())
	}

	// Ties GSTR-1 tax to the output-tax POSTINGS independently.
	if recon.Balanced() != exp.GSTR1.Balanced {
		problems = append(problems, fmt.Sprintf(
			"GSTR-1 balanced (return-vs-postings): got %v expected %v", recon.Balanced(), exp.GSTR1.Balanced))
	}
	if g.FP != exp.GSTR1.FP {
		problems = append(problems, fmt.Sprintf("GSTR-1 fp: got %s expected %s", g.FP, exp.GSTR1.FP))
	}

	tt := g.TaxTotals()
	cmpDecimal(&problems, "GSTR-1 TaxTotals CGST", tt.CGST, exp.GSTR1.TaxTotals.CGST)
	cmpDecimal(&problems, "GSTR-1 TaxTotals SGST", tt.SGST, exp.GSTR1.TaxTotals.SGST)
	cmpDecimal(&problems, "GSTR-1 TaxTotals IGST", tt.IGST, exp.GSTR1.TaxTotals.IGST)
	// Taxable total: sum of HSN txval (the HSN summary covers every invoice).
	taxable := decimal.Zero
	for _, h := range g.HSN.Data {
		taxable = taxable.Add(h.Txval)
	}
	cmpDecimal(&problems, "GSTR-1 TaxTotals taxable", taxable, exp.GSTR1.TaxTotals.Taxable)

	// HSN rows keyed by (hsn,rate).
	type htax struct{ txval, cgst, sgst, igst decimal.Decimal }
	hsnGot := map[string]htax{}
	for _, h := range g.HSN.Data {
		k := h.HsnSc + "@" + h.Rt.String()
		v := hsnGot[k]
		hsnGot[k] = htax{v.txval.Add(h.Txval), v.cgst.Add(h.Camt), v.sgst.Add(h.Samt), v.igst.Add(h.Iamt)}
	}
	hsnSeen := map[string]bool{}
	for _, want := range exp.GSTR1.HSN {
		k := want.HSN + "@" + want.Rate
		hsnSeen[k] = true
		got, ok := hsnGot[k]
		if !ok {
			problems = append(problems, fmt.Sprintf("GSTR-1 HSN@%s (hsn %q): missing from return", want.Rate, want.HSN))
			continue
		}
		label := "GSTR-1 HSN@" + want.Rate
		cmpDecimal(&problems, label+" Taxable", got.txval, want.Taxable)
		cmpDecimal(&problems, label+" CGST", got.cgst, want.CGST)
		cmpDecimal(&problems, label+" SGST", got.sgst, want.SGST)
		cmpDecimal(&problems, label+" IGST", got.igst, want.IGST)
	}
	for k := range hsnGot {
		if !hsnSeen[k] {
			problems = append(problems, fmt.Sprintf("GSTR-1 HSN %s: present in return but not expected", k))
		}
	}

	// B2CS buckets keyed by (sply,pos,rate).
	type btax struct{ txval, cgst, sgst, igst decimal.Decimal }
	b2csGot := map[string]btax{}
	for _, b := range g.B2CS {
		k := b.SplyTy + "|" + b.Pos + "|" + b.Rt.String()
		v := b2csGot[k]
		b2csGot[k] = btax{v.txval.Add(b.Txval), v.cgst.Add(b.Camt), v.sgst.Add(b.Samt), v.igst.Add(b.Iamt)}
	}
	b2csSeen := map[string]bool{}
	for _, want := range exp.GSTR1.B2CS {
		k := want.SplyTy + "|" + want.Pos + "|" + want.Rate
		b2csSeen[k] = true
		got, ok := b2csGot[k]
		if !ok {
			problems = append(problems, fmt.Sprintf("GSTR-1 B2CS %s/%s@%s: missing from return", want.SplyTy, want.Pos, want.Rate))
			continue
		}
		label := fmt.Sprintf("GSTR-1 B2CS %s/%s@%s", want.SplyTy, want.Pos, want.Rate)
		cmpDecimal(&problems, label+" Taxable", got.txval, want.Taxable)
		cmpDecimal(&problems, label+" CGST", got.cgst, want.CGST)
		cmpDecimal(&problems, label+" SGST", got.sgst, want.SGST)
		cmpDecimal(&problems, label+" IGST", got.igst, want.IGST)
	}
	for k := range b2csGot {
		if !b2csSeen[k] {
			problems = append(problems, fmt.Sprintf("GSTR-1 B2CS %s: present in return but not expected", k))
		}
	}

	// B2B rollup per ctin (taxable + tax only; not invoice-wise).
	type ptax struct{ txval, cgst, sgst, igst decimal.Decimal }
	b2bGot := map[string]ptax{}
	for _, p := range g.B2B {
		v := b2bGot[p.CTIN]
		for _, inv := range p.Inv {
			for _, it := range inv.Itms {
				v.txval = v.txval.Add(it.ItmDet.Txval)
				v.cgst = v.cgst.Add(it.ItmDet.Camt)
				v.sgst = v.sgst.Add(it.ItmDet.Samt)
				v.igst = v.igst.Add(it.ItmDet.Iamt)
			}
		}
		b2bGot[p.CTIN] = v
	}
	b2bSeen := map[string]bool{}
	for _, want := range exp.GSTR1.B2B {
		b2bSeen[want.CTIN] = true
		got, ok := b2bGot[want.CTIN]
		if !ok {
			problems = append(problems, fmt.Sprintf("GSTR-1 B2B %q: missing from return", want.CTIN))
			continue
		}
		label := "GSTR-1 B2B " + want.CTIN
		cmpDecimal(&problems, label+" Taxable", got.txval, want.Taxable)
		cmpDecimal(&problems, label+" CGST", got.cgst, want.CGST)
		cmpDecimal(&problems, label+" SGST", got.sgst, want.SGST)
		cmpDecimal(&problems, label+" IGST", got.igst, want.IGST)
	}
	for c := range b2bGot {
		if !b2bSeen[c] {
			problems = append(problems, fmt.Sprintf("GSTR-1 B2B %q: present in return but not expected", c))
		}
	}
	return problems
}

// reconcileGSTR3B resolves the output- and input-tax ledger names, runs
// gstr.Reconcile3B for the period, and diffs the 3B tables (3.1(a), 3.1(d), 3.2,
// 4C) against exp. Every problem string is prefixed "GSTR-3B" so a failure names
// the statement.
//
// It is an INDEPENDENT check of the same books the GSTR-1 step reads: 3.1(a) is
// produced by gstr.Generate3B's own aggregating query, not by re-summing the
// GSTR-1 rollup, so a divergence between the two returns shows up here.
func reconcileGSTR3B(ctx context.Context, gstrSvc *gstr.Service, res LedgerResolver,
	exp ExpectedReconciliation) []string {

	var problems []string
	if gstrSvc == nil {
		return []string{"GSTR-3B: no gstr service supplied but a gstr3b oracle is present"}
	}

	resolve := func(name string) uuid.UUID {
		if name == "" || res == nil {
			return uuid.Nil
		}
		id, err := res.LedgerID(ctx, name)
		if err != nil {
			problems = append(problems, fmt.Sprintf("GSTR-3B taxLedger %q: %v", name, err))
			return uuid.Nil
		}
		return id
	}

	var opts gstr.Options3B
	opts.GSTIN = exp.GSTIN
	if exp.TaxLedgers != nil {
		opts.CGSTLedgerID = resolve(exp.TaxLedgers.CGST)
		opts.SGSTLedgerID = resolve(exp.TaxLedgers.SGST)
		opts.IGSTLedgerID = resolve(exp.TaxLedgers.IGST)
	}
	if exp.ITCLedgers != nil {
		opts.InputCGSTLedgerID = resolve(exp.ITCLedgers.CGST)
		opts.InputSGSTLedgerID = resolve(exp.ITCLedgers.SGST)
		opts.InputIGSTLedgerID = resolve(exp.ITCLedgers.IGST)
	}

	g, recon, err := gstrSvc.Reconcile3B(ctx, exp.Period.From, exp.Period.To, opts)
	if err != nil {
		return append(problems, "GSTR-3B: "+err.Error())
	}

	if recon.Balanced() != exp.GSTR3B.Balanced {
		problems = append(problems, fmt.Sprintf(
			"GSTR-3B balanced (return-vs-postings): got %v expected %v", recon.Balanced(), exp.GSTR3B.Balanced))
	}
	if g.FP != exp.GSTR3B.FP {
		problems = append(problems, fmt.Sprintf("GSTR-3B fp: got %s expected %s", g.FP, exp.GSTR3B.FP))
	}

	// Table 3.1(a) — outward taxable supplies.
	a := g.T31.OsupDet
	cmpDecimal(&problems, "GSTR-3B 3.1(a) taxable", a.Txval, exp.GSTR3B.Outward.Taxable)
	cmpDecimal(&problems, "GSTR-3B 3.1(a) CGST", a.Camt, exp.GSTR3B.Outward.CGST)
	cmpDecimal(&problems, "GSTR-3B 3.1(a) SGST", a.Samt, exp.GSTR3B.Outward.SGST)
	cmpDecimal(&problems, "GSTR-3B 3.1(a) IGST", a.Iamt, exp.GSTR3B.Outward.IGST)

	// Table 3.1(d) — inward supplies liable to reverse charge.
	d := g.T31.IsupRev
	cmpDecimal(&problems, "GSTR-3B 3.1(d) taxable", d.Txval, exp.GSTR3B.RCMInward.Taxable)
	cmpDecimal(&problems, "GSTR-3B 3.1(d) CGST", d.Camt, exp.GSTR3B.RCMInward.CGST)
	cmpDecimal(&problems, "GSTR-3B 3.1(d) SGST", d.Samt, exp.GSTR3B.RCMInward.SGST)
	cmpDecimal(&problems, "GSTR-3B 3.1(d) IGST", d.Iamt, exp.GSTR3B.RCMInward.IGST)

	// Table 3.2 — inter-state supplies to unregistered persons, keyed by POS.
	// Checked in BOTH directions so an extra row is as loud as a missing one.
	type isup struct{ txval, igst decimal.Decimal }
	got32 := map[string]isup{}
	for _, r := range g.T32 {
		v := got32[r.Pos]
		got32[r.Pos] = isup{v.txval.Add(r.Txval), v.igst.Add(r.Iamt)}
	}
	seen32 := map[string]bool{}
	for _, want := range exp.GSTR3B.InterUnreg {
		seen32[want.Pos] = true
		got, ok := got32[want.Pos]
		if !ok {
			problems = append(problems, fmt.Sprintf("GSTR-3B 3.2 pos %s: missing from return", want.Pos))
			continue
		}
		label := "GSTR-3B 3.2 pos " + want.Pos
		cmpDecimal(&problems, label+" taxable", got.txval, want.Taxable)
		cmpDecimal(&problems, label+" IGST", got.igst, want.IGST)
	}
	for pos := range got32 {
		if !seen32[pos] {
			problems = append(problems, fmt.Sprintf("GSTR-3B 3.2 pos %s: present in return but not expected", pos))
		}
	}

	// Table 4(C) — net ITC available. The oracle's `taxable` slot is unused for
	// ITC (table 4 carries tax heads only), so it is not compared.
	net := g.T4.ItcNet
	cmpDecimal(&problems, "GSTR-3B ITC CGST", net.Camt, exp.GSTR3B.ITC.CGST)
	cmpDecimal(&problems, "GSTR-3B ITC SGST", net.Samt, exp.GSTR3B.ITC.SGST)
	cmpDecimal(&problems, "GSTR-3B ITC IGST", net.Iamt, exp.GSTR3B.ITC.IGST)

	// Tables 4(B) and 4(D), compared only when the fixture states them. They
	// are read off the RECONCILIATION rather than re-summed here so that the
	// figure checked is the same one Balanced() rests on.
	if want := exp.GSTR3B.ITCReversed; want != nil {
		cmpDecimal(&problems, "GSTR-3B ITC reversed CGST", recon.ITCReversed.CGST, want.CGST)
		cmpDecimal(&problems, "GSTR-3B ITC reversed SGST", recon.ITCReversed.SGST, want.SGST)
		cmpDecimal(&problems, "GSTR-3B ITC reversed IGST", recon.ITCReversed.IGST, want.IGST)
	}
	if want := exp.GSTR3B.ITCIneligible; want != nil {
		cmpDecimal(&problems, "GSTR-3B ITC ineligible CGST", recon.ITCIneligible.CGST, want.CGST)
		cmpDecimal(&problems, "GSTR-3B ITC ineligible SGST", recon.ITCIneligible.SGST, want.SGST)
		cmpDecimal(&problems, "GSTR-3B ITC ineligible IGST", recon.ITCIneligible.IGST, want.IGST)
	}

	return problems
}

// reconcileOutstandings diffs receivables and payables (per-party Pending and,
// when the oracle lists bills, per-ref Pending + DueDate).
func reconcileOutstandings(ctx context.Context, rep *reporting.Service, exp ExpectedReconciliation) []string {
	var problems []string
	asOn := exp.Outstandings.AsOn

	check := func(kind reporting.OutstandingKind, want []ExpectedParty, label string) {
		if len(want) == 0 {
			return
		}
		got, err := rep.Outstandings(ctx, companyIDFromCtx(ctx), kind, asOn, nil)
		if err != nil {
			problems = append(problems, fmt.Sprintf("OUTSTANDING %s: %v", label, err))
			return
		}
		byName := map[string]reporting.PartyOutstanding{}
		for _, p := range got.Parties {
			byName[p.LedgerName] = p
		}
		for _, wp := range want {
			gp, ok := byName[wp.Ledger]
			if !ok {
				problems = append(problems, fmt.Sprintf("OUTSTANDING %s %q: missing from report (expected %s)", label, wp.Ledger, wp.Pending))
				continue
			}
			cmpDecimal(&problems, fmt.Sprintf("OUTSTANDING %s %q", label, wp.Ledger), gp.Pending, wp.Pending)
			if len(wp.Bills) > 0 {
				byRef := map[string]reporting.Bill{}
				for _, b := range gp.Bills {
					byRef[b.RefName] = b
				}
				for _, wb := range wp.Bills {
					gb, ok := byRef[wb.Ref]
					if !ok {
						problems = append(problems, fmt.Sprintf("OUTSTANDING %s %q bill %q: missing (expected %s)", label, wp.Ledger, wb.Ref, wb.Pending))
						continue
					}
					cmpDecimal(&problems, fmt.Sprintf("OUTSTANDING %s %q bill %q", label, wp.Ledger, wb.Ref), gb.Pending, wb.Pending)
					if wb.DueDate != "" && gb.DueDate != wb.DueDate {
						problems = append(problems, fmt.Sprintf(
							"OUTSTANDING %s %q bill %q dueDate: got %s expected %s", label, wp.Ledger, wb.Ref, gb.DueDate, wb.DueDate))
					}
				}
			}
		}
	}
	check(reporting.Receivables, exp.Outstandings.Receivables, "receivable")
	check(reporting.Payables, exp.Outstandings.Payables, "payable")
	return problems
}

// cmpDecimal parses the expected money string and appends a "got X expected Y"
// problem under label when it diverges from got. A malformed expected string is
// itself a problem (the oracle is wrong).
func cmpDecimal(problems *[]string, label string, got decimal.Decimal, want string) {
	w, err := decimal.NewFromString(want)
	if err != nil {
		*problems = append(*problems, fmt.Sprintf("%s: malformed expected value %q: %v", label, want, err))
		return
	}
	if !got.Equal(w) {
		*problems = append(*problems, fmt.Sprintf("%s: got %s expected %s", label, got.StringFixed(4), w.StringFixed(4)))
	}
}

// companyIDFromCtx extracts the company bound to ctx for the Outstandings call,
// which takes the id explicitly. The reporting scope guard re-validates it.
func companyIDFromCtx(ctx context.Context) uuid.UUID {
	if id, ok := db.CompanyFromContext(ctx); ok {
		return id
	}
	return uuid.Nil
}
