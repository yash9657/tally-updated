package payroll

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

// payroll_test.go — the tests that keep the EPFO liability on the books.
//
// ===========================================================================
// THE DEFECT THEY EXIST FOR
// ===========================================================================
// AggregateLegs was fixed to DEBIT the EDLI premium and the EPF/EDLI
// administrative charges, and the fix had ZERO NON-TEST CALLERS. The product
// posted its pay runs through payrolldb.buildPostings, which derived its own
// legs from the payslip columns — and there were no columns for those three, so
// every pay run the product wrote omitted the whole employer charge. Under the
// rates in force before 29 June 2026 that is 0.50% of EDLI wages plus 0.50% of
// EPF wages: about Rs 150 per member per month of EPFO liability that never
// reached the ledger, on a challan that therefore does not reconcile against
// the books.
//
// Two derivations that can disagree is the defect, and it has two halves — the
// wrong builder, and a store that cannot supply what the right builder needs.
// So there are three guards:
//
//	TestPayRunPostingsCarryTheEDLIPremiumAndTheAdminCharges runs the SAME leg
//	builder the product runs, over the SAME reconstruction of stored rows the
//	product feeds it, and asserts the charges are debited, credited and
//	balanced.
//
//	TestExactlyOneLegBuilderDerivesTheVoucher parses payrolldb and fails if a
//	second derivation ever reappears there — which is the only way the first
//	test could go back to proving something nothing calls.
//
//	TestEveryPersistedPayslipFigureReachesTheLegBuilder fails if the store
//	stops supplying a figure, because the one builder can only post what it is
//	given — that is how three charges with no column went unbooked.

// notifiedChargeMonth is a wage month in a window where the EDLI premium and
// the EPF administrative charge ARE notified (ss-2025-11, 21 Nov 2025 to 29 Jun
// 2026: EDLI 0.50% of EDLI wages, EPF admin 0.50% of EPF wages, EDLI admin
// waived at 0% since 1 Apr 2017 — see the citations on those Rate rows in
// rates.go).
//
// It is deliberately NOT the current month. Under the EPF/EDLI Schemes 2026 all
// three rates are ConfUnverified, the engine DEFERS them, and a test written on
// today's month would assert nothing at all while looking like it asserted
// everything. Pay runs for months in this window are ordinary work — a month
// re-run after a correction, or a company onboarding its back months.
var notifiedChargeMonth = WageMonth{Year: 2026, Month: time.May}

// TestPayRunPostingsCarryTheEDLIPremiumAndTheAdminCharges is the regression
// test for the unbooked EPFO liability.
//
// It exercises the product's path: PayslipFromPersisted (what payrolldb calls
// on each stored payslip row) followed by AggregateLegs (the one leg builder).
// The figures are the ENGINE'S — PFDerive under the real ruleset for the month
// — so the test also fails if the rate table stops notifying a charge that the
// citations say was payable.
func TestPayRunPostingsCarryTheEDLIPremiumAndTheAdminCharges(t *testing.T) {
	month := notifiedChargeMonth
	ss, err := SocialSecurityForMonth(month)
	if err != nil {
		t.Fatalf("resolve the social-security ruleset for %s: %v", month, err)
	}

	// Two employees, both on the ceiling base, so the charges are the real
	// per-member figures and the run is not a single-row special case.
	const employees = 2
	pf, err := PFDerive(PFInput{
		Month:         month,
		StatutoryWage: decimal.RequireFromString("30000"),
		Base:          PFBaseCeiling,
		EPSMember:     true,
	}, ss)
	if err != nil {
		t.Fatalf("PFDerive for %s: %v", month, err)
	}

	amountOf := func(name string) decimal.Decimal {
		c, ok := pf.ComponentByName(name)
		if !ok {
			return decimal.Zero
		}
		return c.Amount
	}
	var (
		eeStat   = amountOf(ComponentEPFEmployee)
		erStat   = amountOf(ComponentEPFEmployer)
		eps      = amountOf(ComponentEPS)
		edli     = amountOf(ComponentEDLIContribution)
		edliAdm  = amountOf(ComponentEDLIAdminCharges)
		epfAdm   = amountOf(ComponentEPFAdminCharges)
		perSlip  = edli.Add(edliAdm).Add(epfAdm)
		chargesR = perSlip.Mul(decimal.NewFromInt(employees))
	)

	// The ESTABLISHMENT-level floor on the EPF administrative charge, derived
	// here independently of AggregateLegs so that the expectations below are not
	// the implementation restated. Two members at the ceiling do not reach the
	// Rs 500 monthly minimum between them, so this run carries a top-up.
	epfAdmRun := epfAdm.Mul(decimal.NewFromInt(employees))
	adminTopUp := decimal.Zero
	if min, err := ss.EPFAdminChargeMin.Use(); err == nil && epfAdmRun.LessThan(min) {
		adminTopUp = min.Sub(epfAdmRun)
	}
	chargesR = chargesR.Add(adminTopUp)

	// ANTI-VACUITY GATE. If the ruleset for this month notifies none of the
	// three, every assertion below holds trivially and the test proves nothing.
	// Fail loudly and say which month to move to rather than pass silently.
	if !perSlip.IsPositive() {
		t.Fatalf("the %s ruleset (%s) notifies none of the EDLI premium, the EDLI administrative charge or the EPF "+
			"administrative charge, so this test would assert nothing. Move notifiedChargeMonth to a window where at "+
			"least one is notified — a test that cannot see the charge cannot see it being dropped either",
			month, ss.Version)
	}

	// The stored rows, exactly as payrolldb holds them: flat columns, one per
	// employee, with the three employer charges in the columns migration 0036
	// added.
	gross := decimal.RequireFromString("30000")
	tds := decimal.RequireFromString("1200")
	net := gross.Sub(tds).Sub(eeStat)
	stored := make([]Payslip, 0, employees)
	for i := 0; i < employees; i++ {
		slip, err := PayslipFromPersisted(PersistedPayslip{
			EmployeeRef:                  fmt.Sprintf("EMP-%03d", i+1),
			Month:                        month,
			SocialSecurityRuleSetVersion: ss.Version,
			IncomeTaxRuleSetVersion:      "it-ty-2026-27",
			GrossEarnings:                gross,
			NetPay:                       net,
			TDS:                          tds,
			EPFEmployeeStatutory:         eeStat,
			EPFEmployerStatutory:         erStat,
			EPSEmployer:                  eps,
			EDLIContribution:             edli,
			EDLIAdminCharges:             edliAdm,
			EPFAdminCharges:              epfAdm,
		})
		if err != nil {
			t.Fatalf("rebuild the stored payslip for employee %d: %v", i+1, err)
		}
		stored = append(stored, slip)
	}

	legs, err := AggregateLegs(stored)
	if err != nil {
		t.Fatalf("the %s pay run for %d employees will not post: %v\n"+
			"The EDLI premium (Rs %s per member) and the administrative charges (Rs %s per member) are EMPLOYER "+
			"liabilities to the fund. They must be DEBITED to %q and credited to %q — never dropped to make the "+
			"entry balance, because dropping them deletes Rs %s of EPFO liability from the books",
			month, employees, err, edli.String(), edliAdm.Add(epfAdm).String(),
			LedgerEmployerPFContribution, LedgerPFPayable, chargesR.String())
	}

	legAmount := func(ledgerName, direction string, tag ComponentTag) decimal.Decimal {
		total := decimal.Zero
		for _, l := range legs.Legs {
			if l.Ledger == ledgerName && l.Direction == direction && l.Tag == tag {
				total = total.Add(l.Amount)
			}
		}
		return total
	}

	// (1) THE DEBIT. The employer's PF expense is its contribution into the
	// member's account PLUS the charges that go to the fund but not to the
	// member.
	memberAccount := erStat.Add(eps).Mul(decimal.NewFromInt(employees))
	wantDebit := memberAccount.Add(chargesR)
	gotDebit := legAmount(LedgerEmployerPFContribution, DirectionDebit, TagStatutory)
	if !gotDebit.Equal(wantDebit) {
		t.Errorf("the pay run for %s books Rs %s of employer PF but should book Rs %s: it omits Rs %s of EDLI premium "+
			"and Rs %s of EPF/EDLI administrative charges across %d employees. The EPFO liability is understated by "+
			"Rs %s for this one month, the employer's wage cost is understated by the same amount, and the challan "+
			"will not reconcile against the books",
			month, gotDebit.String(), wantDebit.String(),
			edli.Mul(decimal.NewFromInt(employees)).String(),
			edliAdm.Add(epfAdm).Mul(decimal.NewFromInt(employees)).String(),
			employees, wantDebit.Sub(gotDebit).String())
	}

	// (2) THE CREDIT. The charges are part of the EPFO remittance, so they are
	// in the statutory PF Payable pool. This is the leg the challan is paid
	// from.
	wantCredit := eeStat.Add(erStat).Add(eps).Mul(decimal.NewFromInt(employees)).Add(chargesR)
	gotCredit := legAmount(LedgerPFPayable, DirectionCredit, TagStatutory)
	if !gotCredit.Equal(wantCredit) {
		t.Errorf("the pay run for %s credits Rs %s to %q but owes the fund Rs %s: Rs %s of EDLI premium and "+
			"administrative charges is missing from the remittance. The next EPFO challan is short by that amount "+
			"and the employer is in default of it",
			month, gotCredit.String(), LedgerPFPayable, wantCredit.String(), wantCredit.Sub(gotCredit).String())
	}

	// (2a) The establishment floor is reported by name. The operator reconciling
	// the ECR (which is per member) against the challan (which is not) has to be
	// able to see the establishment-level difference without deducing it.
	if !legs.EPFAdminChargeTopUp.Equal(adminTopUp) {
		t.Errorf("the %s pay run reports Rs %s of EPF administrative-charge top-up, want Rs %s: %d members at the "+
			"ceiling book Rs %s between them against a statutory minimum of Rs 500 per establishment per month",
			month, legs.EPFAdminChargeTopUp.String(), adminTopUp.String(), employees, epfAdmRun.String())
	}

	// (3) AND IT STILL BALANCES, with the charges in. AggregateLegs refuses an
	// unbalanced set, so reaching here already proves it; asserting it makes
	// the guarantee visible in the test rather than implied by an absent error.
	if !legs.DebitTotal.Equal(legs.CreditTotal) {
		t.Fatalf("the %s pay run's voucher does not balance: debits Rs %s, credits Rs %s, difference Rs %s",
			month, legs.DebitTotal.String(), legs.CreditTotal.String(),
			legs.DebitTotal.Sub(legs.CreditTotal).String())
	}

	t.Logf("%s (%s): %d employees, employer PF debit Rs %s of which Rs %s is EDLI + administrative charges; "+
		"PF Payable credit Rs %s; voucher balances at Rs %s",
		month, ss.Version, employees, gotDebit.String(), chargesR.String(),
		gotCredit.String(), legs.DebitTotal.String())
}

// TestPayslipFromPersistedRefusesARowThatCannotBalance pins the per-employee
// refusal.
//
// The aggregate balance check can only ever say "the run is short by X". This
// says WHICH EMPLOYEE'S row is wrong, before a voucher exists — which is the
// only form of the message a payroll operator can act on.
func TestPayslipFromPersistedRefusesARowThatCannotBalance(t *testing.T) {
	base := PersistedPayslip{
		EmployeeRef:                  "EMP-001",
		Month:                        notifiedChargeMonth,
		SocialSecurityRuleSetVersion: "ss-2025-11",
		GrossEarnings:                decimal.RequireFromString("30000"),
		NetPay:                       decimal.RequireFromString("27000"),
		TDS:                          decimal.RequireFromString("1200"),
		EPFEmployeeStatutory:         decimal.RequireFromString("1800"),
	}
	if _, err := PayslipFromPersisted(base); err != nil {
		t.Fatalf("a row whose withheld heads explain gross - net must be accepted: %v", err)
	}

	// Net pay a thousand rupees too high: the employee was paid money that no
	// credit leg carries.
	short := base
	short.NetPay = decimal.RequireFromString("28000")
	if _, err := PayslipFromPersisted(short); !errors.Is(err, ErrPersistedPayslipInconsistent) {
		t.Fatalf("a payslip whose net pay exceeds gross less the withheld heads by Rs 1000 must be refused by name; got %v", err)
	}

	// A negative statutory charge. It would post on the wrong side of its own
	// expense and still balance.
	neg := base
	neg.EDLIContribution = decimal.RequireFromString("-75")
	if _, err := PayslipFromPersisted(neg); !errors.Is(err, ErrPersistedPayslipInconsistent) {
		t.Fatalf("a negative EDLI premium must be refused by name; got %v", err)
	}
}

// ---------------------------------------------------------------------------
// The structural half: there may be only ONE leg builder.
// ---------------------------------------------------------------------------

// payrolldbDir is the DB-facing package this test reads. It is a relative path
// because `go test` runs with the package directory as the working directory,
// which is the same assumption packageGoFiles already makes.
const payrolldbDir = "payrolldb"

// TestExactlyOneLegBuilderDerivesTheVoucher fails the build if payrolldb ever
// derives posting legs again instead of calling AggregateLegs.
//
// WHY A STRUCTURAL TEST AND NOT A BEHAVIOURAL ONE. The defect was not a wrong
// number in a function; it was a SECOND function computing the same thing, in a
// package whose tests all needed a database. Nothing about the pure package's
// figures was wrong, and no amount of testing AggregateLegs would have found
// it — the product simply did not call it. The only property that closes the
// hole is "there is one derivation", and that is a property of the source.
//
// It checks three things about internal/payroll/payrolldb:
//
//	(a) buildPostings calls payroll.AggregateLegs;
//	(b) every ledger.PostingInput built anywhere in the package is built inside
//	    buildPostings, so no other function can post a leg of its own;
//	(c) the Amount on each of those literals is read straight off a leg
//	    (an X.Amount selector) rather than summed from columns, so buildPostings
//	    cannot call the builder and then post something else.
func TestExactlyOneLegBuilderDerivesTheVoucher(t *testing.T) {
	entries, err := os.ReadDir(payrolldbDir)
	if err != nil {
		t.Fatalf("read %s: %v. This test must be able to see the DB-facing package; if it moved, follow it here rather than deleting the test", payrolldbDir, err)
	}

	fset := token.NewFileSet()
	var (
		scanned          int
		sawBuildPostings bool
		callsAggregate   bool
		literals         int
	)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(payrolldbDir, e.Name())
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		scanned++

		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			inBuilder := fn.Name.Name == "buildPostings"
			if inBuilder {
				sawBuildPostings = true
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch node := n.(type) {
				case *ast.CallExpr:
					if inBuilder && isSelectorCall(node.Fun, "payroll", "AggregateLegs") {
						callsAggregate = true
					}
				case *ast.CompositeLit:
					if !isQualifiedType(node.Type, "ledger", "PostingInput") {
						return true
					}
					literals++
					if !inBuilder {
						t.Errorf("%s:%d builds a ledger.PostingInput inside %s. A pay run's legs may be derived in "+
							"exactly ONE place — payroll.AggregateLegs, mapped onto ledger ids by buildPostings — "+
							"because two derivations disagreed once already and the books lost the whole EDLI premium "+
							"and the EPF/EDLI administrative charges for every pay run the product posted",
							path, fset.Position(node.Pos()).Line, fn.Name.Name)
						return true
					}
					for _, el := range node.Elts {
						kv, ok := el.(*ast.KeyValueExpr)
						if !ok {
							continue
						}
						key, ok := kv.Key.(*ast.Ident)
						if !ok || key.Name != "Amount" {
							continue
						}
						sel, ok := kv.Value.(*ast.SelectorExpr)
						if !ok || sel.Sel.Name != "Amount" {
							t.Errorf("%s:%d posts an Amount that is not read straight off a leg returned by "+
								"payroll.AggregateLegs. buildPostings resolves ledger IDS; the amounts and the sides "+
								"are the leg builder's, and an amount computed here is a second derivation by another name",
								path, fset.Position(kv.Value.Pos()).Line)
						}
					}
				}
				return true
			})
		}
	}

	if scanned < 3 {
		t.Fatalf("scanned only %d non-test files in %s; the scan is vacuous", scanned, payrolldbDir)
	}
	if !sawBuildPostings {
		t.Fatalf("found no buildPostings in %s. If the pay run's posting path was renamed, point this test at the new "+
			"name — the property it pins (ONE leg builder) is what keeps the EDLI premium and the administrative "+
			"charges on the books", payrolldbDir)
	}
	if !callsAggregate {
		t.Errorf("buildPostings does not call payroll.AggregateLegs, so the product is deriving its own pay-run legs " +
			"again. That is exactly the split that left every pay run short of the EDLI premium and the EPF/EDLI " +
			"administrative charges — roughly Rs 150 per member per month of EPFO liability, unbooked and unremitted")
	}
	if literals == 0 {
		t.Fatalf("the AST walk found no ledger.PostingInput literal anywhere in %s, so it would not have noticed a "+
			"second leg derivation either; the walk is broken", payrolldbDir)
	}
	if !t.Failed() {
		t.Logf("one leg builder: %d files scanned, %d ledger.PostingInput literal(s), all inside buildPostings, which calls payroll.AggregateLegs", scanned, literals)
	}
}

// TestEveryPersistedPayslipFigureReachesTheLegBuilder closes the other half of
// the hole.
//
// Routing payrolldb through AggregateLegs is not enough on its own: the builder
// can only post what the store hands it, so a field the store stops filling is
// a liability that silently leaves the books WITHOUT any second leg derivation
// appearing. That is the original defect in its purest form — the store had no
// EDLI column, so the fix that debited EDLI had nothing to debit.
//
// So every field of PersistedPayslip (enumerated by reflection, so a field
// added later is covered the day it is added) must be named inside
// buildPostings.
func TestEveryPersistedPayslipFigureReachesTheLegBuilder(t *testing.T) {
	body, path := buildPostingsBody(t)

	// Every field name mentioned inside buildPostings, whether as p.Field, as
	// &p.Field in a Scan, or as a key in a composite literal.
	named := map[string]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.SelectorExpr:
			named[node.Sel.Name] = true
		case *ast.KeyValueExpr:
			if id, ok := node.Key.(*ast.Ident); ok {
				named[id.Name] = true
			}
		}
		return true
	})

	typ := reflect.TypeOf(PersistedPayslip{})
	if typ.NumField() == 0 {
		t.Fatal("PersistedPayslip has no fields; the reflection walk is vacuous")
	}
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if named[f.Name] {
			continue
		}
		t.Errorf("%s never mentions PersistedPayslip.%s, so the stored figure never reaches payroll.AggregateLegs "+
			"and never reaches a posting leg. A figure the store does not supply is a figure the voucher cannot "+
			"carry: that is exactly how the EDLI premium and the EPF/EDLI administrative charges — about Rs 150 per "+
			"member per month — were computed, shown to nobody and posted nowhere, leaving the EPFO liability off "+
			"the books and the challan unreconcilable",
			path, f.Name)
	}
	if !t.Failed() {
		t.Logf("all %d PersistedPayslip figures are supplied by buildPostings", typ.NumField())
	}
}

// buildPostingsBody returns the parsed body of payrolldb.buildPostings and the
// path it was read from.
func buildPostingsBody(t *testing.T) (*ast.BlockStmt, string) {
	t.Helper()
	entries, err := os.ReadDir(payrolldbDir)
	if err != nil {
		t.Fatalf("read %s: %v", payrolldbDir, err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(payrolldbDir, e.Name())
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && fn.Name.Name == "buildPostings" && fn.Body != nil {
				return fn.Body, path
			}
		}
	}
	t.Fatalf("found no buildPostings in %s; the pay run's posting path is what these tests pin", payrolldbDir)
	return nil, ""
}

// isSelectorCall reports whether an expression is the call `pkg.Name`.
func isSelectorCall(fun ast.Expr, pkg, name string) bool {
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == pkg
}

// isQualifiedType reports whether a composite-literal type is `pkg.Name`.
func isQualifiedType(expr ast.Expr, pkg, name string) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == pkg
}
