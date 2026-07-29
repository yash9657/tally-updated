package payrolldb_test

// compute_test.go — THE WIRE, end to end.
//
// Every other test in this package feeds RunPayRun figures a human wrote down.
// That is right for the posting seam (a hand-derived fixture cannot pass for a
// wrong engine) and it is exactly wrong for the question these tests ask, which
// is whether internal/payroll RUNS AT ALL on the path that creates a payslip.
//
// So nothing here supplies a rupee. The tests set up a salary structure and an
// election, call ComputeAndRunPayRun, and assert on the PERSISTED PAYSLIP and
// the POSTED VOUCHER against figures derived BY HAND from the statute and the
// shipped rate table — reproduced in the comment above each fixture so a reader
// can check them without running anything.
//
// The second test is the one that matters most. It proves that a slip which
// satisfies BOTH of SlipInput.validate's self-consistency identities and is
// nonetheless not what the engine computed is REFUSED, and that nothing is
// written when it is.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/payroll"
	"github.com/khata/cored/internal/payroll/payrolldb"
	"github.com/khata/cored/internal/platform/db"
)

// ---------------------------------------------------------------------------
// THE FIXTURE, AND THE HAND DERIVATION OF EVERY FIGURE IT PRODUCES.
//
// Wage month: July 2026 -> social-security ruleset "ss-2026-07" (EPF 12%, EPS
// 8.33%, all three wage ceilings 15,000; ESI 0.75/3.25, coverage ceiling
// 21,000, Rule 52 daily limit 176) and income-tax ruleset "it-ty-2026-27".
//
// ---------------------------------------------------------------------------
// EMPLOYEE ONE — a flat structure, in force from 1 April 2026.
//
//	BASIC              12,000
//	COMM (commission)   4,000   (EXCLUDED from wages by s.2(88) of the Code,
//	                             and yet INCLUDED in the ESI-reckonable wage
//	                             under s.2(22) of the ESI Act — a monthly
//	                             commission is additional remuneration paid at
//	                             intervals not exceeding two months)
//	OTHR (other allw.)  2,000   (INCLUDED — the s.2(88) list is exhaustive and
//	                             an unclassified allowance is not on it)
//	total              18,000
//
// The commission line is the fixture's point of leverage: it is the cleanest
// component that ONE statute excludes and the OTHER includes, so a payslip that
// computed the ESI base off the Code's statutory wage would come out at 14,000
// instead of 18,000 and under-contribute for both sides. Conflating the two
// definitions is the commonest ESI defect and it is invisible in the output,
// because 14,000 is a perfectly plausible number.
//
//	s.2(88): included 14,000 (BASIC + OTHR), excluded 4,000 (COMM). The 50%
//	floor is 9,000 and the exclusions (4,000) do not exceed it, so nothing is
//	deemed back:
//	  STATUTORY WAGE = 14,000
//
//	EPF (base CEILING): min(14,000, 15,000) = 14,000
//	  employee  12% x 14,000            = 1,680
//	  employer  12% x 14,000            = 1,680 total, of which
//	  EPS       8.33% x 14,000 = 1,166.2 -> 1,166 (nearest rupee)
//	  employer EPF = 1,680 - 1,166       =   514
//
//	ESI: the coverage latch tests the wage in force on 1 April 2026 (the start
//	of the Apr-Sep contribution period) = 18,000, which does not exceed 21,000,
//	so COVERED for the whole period. Contribution is on the month's
//	ESI-reckonable wage, 18,000 — the commission IS an ESI wage even though it
//	is not a Code wage (see compute.go):
//	  Rule 52 threshold 176 x 31 = 5,456; 18,000 exceeds it, so no exemption
//	  employee  0.75% x 18,000 = 135      ->   135 (Rule 51 rounds UP)
//	  employer  3.25% x 18,000 = 585      ->   585
//
//	s.392: annual estimate 18,000 x 12 = 216,000, less the new regime's
//	standard deduction, is far below the s.156 rebate limit -> TDS = 0.
//
//	gross 18,000 - (1,680 + 135 + 0) = NET 16,185
//
// ---------------------------------------------------------------------------
// EMPLOYEE TWO — A MID-PERIOD RAISE, WHICH IS THE POINT OF THE FIXTURE.
//
//	1 Apr 2026 - 1 Jun 2026 (half-open): BASIC 12,000 + COMM 4,000 + OTHR 2,000
//	from 1 Jun 2026:                     BASIC 17,000 + COMM 5,000 + OTHR 3,000
//
//	July's wage is 25,000, which EXCEEDS the 21,000 ESI coverage ceiling. An
//	implementation that tested the ceiling against THIS MONTH would compute no
//	ESI at all. The statute latches coverage at the START of the contribution
//	period, where the wage was 18,000, and holds it for the whole period — so
//	this employee IS covered in July and both shares are payable. That is the
//	single assertion in this file that cannot be satisfied without loading the
//	FULL wage-revision history, and it is why the fixture has a raise in it.
//
//	s.2(88): included 20,000, excluded 5,000, floor 12,500 -> no add-back
//	  STATUTORY WAGE = 20,000
//	EPF (CEILING): min(20,000, 15,000) = 15,000
//	  employee  12% x 15,000                = 1,800
//	  EPS       8.33% x 15,000 = 1,249.5    -> 1,250 (half away from zero)
//	  employer EPF = 1,800 - 1,250          =   550
//	ESI on 25,000 (no contribution ceiling — only coverage has one):
//	  employee  0.75% x 25,000 = 187.5      ->   188 (UP)
//	  employer  3.25% x 25,000 = 812.5      ->   813 (UP)
//	TDS = 0 for the same reason as employee one.
//
//	gross 25,000 - (1,800 + 188) = NET 23,012
//
// ---------------------------------------------------------------------------
// THE VOUCHER
//
//	Dr Salaries & Wages           18,000 + 25,000            = 43,000
//	Dr Employer PF Contribution   (514+1,166) + (550+1,250)  =  3,480
//	Dr Employer ESI Contribution  585 + 813                  =  1,398
//	  Cr Salary Payable           16,185 + 23,012            = 39,197
//	  Cr PF Payable               3,360 + 3,600              =  6,960
//	  Cr ESI Payable              720 + 1,001                =  1,721
//	                                              47,878 = 47,878
//
// TDS on Salary Payable carries NO leg: a zero leg is omitted rather than
// posted, so its absence is an assertion of its own below.
// ---------------------------------------------------------------------------

// computeMonth is the wage month every test in this file runs.
var computeMonth = payroll.WageMonth{Year: 2026, Month: 7}

// setStructureRow inserts one effective-dated salary component.
func setStructureRow(t *testing.T, svc *payrolldb.Service, ctx context.Context,
	empID uuid.UUID, from, to, code string, typ payroll.ComponentType, amount string) {
	t.Helper()
	if _, err := svc.SetSalaryComponent(ctx, payrolldb.SalaryComponent{
		EmployeeID: empID, EffectiveFrom: from, EffectiveTo: to,
		Code: code, Type: typ, Amount: dec(amount),
	}); err != nil {
		t.Fatalf("set salary component %s for %s: %v", code, empID, err)
	}
}

// flatStructure lays down the 18,000 structure over an open-ended window.
func flatStructure(t *testing.T, svc *payrolldb.Service, ctx context.Context, empID uuid.UUID) {
	t.Helper()
	setStructureRow(t, svc, ctx, empID, "2026-04-01", "", "BASIC", payroll.ComponentBasic, "12000")
	setStructureRow(t, svc, ctx, empID, "2026-04-01", "", "COMM", payroll.ComponentCommission, "4000")
	setStructureRow(t, svc, ctx, empID, "2026-04-01", "", "OTHR", payroll.ComponentOtherAllowance, "2000")
}

// raisedStructure lays down the 18,000 structure closed at 1 June 2026 and the
// 25,000 structure from that date — the mid-period raise the ESI latch is
// tested against.
func raisedStructure(t *testing.T, svc *payrolldb.Service, ctx context.Context, empID uuid.UUID) {
	t.Helper()
	setStructureRow(t, svc, ctx, empID, "2026-04-01", "2026-06-01", "BASIC", payroll.ComponentBasic, "12000")
	setStructureRow(t, svc, ctx, empID, "2026-04-01", "2026-06-01", "COMM", payroll.ComponentCommission, "4000")
	setStructureRow(t, svc, ctx, empID, "2026-04-01", "2026-06-01", "OTHR", payroll.ComponentOtherAllowance, "2000")
	setStructureRow(t, svc, ctx, empID, "2026-06-01", "", "BASIC", payroll.ComponentBasic, "17000")
	setStructureRow(t, svc, ctx, empID, "2026-06-01", "", "COMM", payroll.ComponentCommission, "5000")
	setStructureRow(t, svc, ctx, empID, "2026-06-01", "", "OTHR", payroll.ComponentOtherAllowance, "3000")
}

func setElection(t *testing.T, svc *payrolldb.Service, ctx context.Context, empID uuid.UUID) {
	t.Helper()
	if _, err := svc.SetStatutoryElection(ctx, payrolldb.StatutoryElection{
		EmployeeID: empID, EffectiveFrom: "2026-04-01",
		PFBase: payroll.PFBaseCeiling, EPSMember: true, ESIApplicable: true,
		Regime: payroll.RegimeNew,
	}); err != nil {
		t.Fatalf("set statutory election for %s: %v", empID, err)
	}
}

// computeFixture creates the two employees and their structures.
func computeFixture(t *testing.T, svc *payrolldb.Service, ctx context.Context, suffix string) (one, two uuid.UUID) {
	t.Helper()
	one = createEmployee(t, svc, ctx, "E-CMP-1-"+suffix, "Flat Structure")
	two = createEmployee(t, svc, ctx, "E-CMP-2-"+suffix, "Mid Period Raise")
	flatStructure(t, svc, ctx, one)
	raisedStructure(t, svc, ctx, two)
	setElection(t, svc, ctx, one)
	setElection(t, svc, ctx, two)
	return one, two
}

// persistedSlip reads back the payslip row for one employee, as columns rather
// than through a read model — a projection that dropped a column would make
// this test agree with itself.
func persistedSlip(t *testing.T, pool *db.Pool, ctx context.Context, payRunID, employeeID uuid.UUID) map[string]decimal.Decimal {
	t.Helper()
	cols := []string{
		"gross_earnings", "total_deductions", "net_pay",
		"statutory_wages", "epf_wages", "eps_wages", "edli_wages", "esi_wages",
		"epf_employee_statutory", "epf_employee_voluntary",
		"epf_employer_statutory", "epf_employer_voluntary", "eps_employer",
		"esi_employee", "esi_employer", "tds", "days_paid",
	}
	out := map[string]decimal.Decimal{}
	if err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		vals := make([]decimal.Decimal, len(cols))
		dest := make([]any, len(cols))
		for i := range vals {
			dest[i] = &vals[i]
		}
		if err := tx.QueryRow(ctx,
			`SELECT gross_earnings, total_deductions, net_pay,
			        statutory_wages, epf_wages, eps_wages, edli_wages, esi_wages,
			        epf_employee_statutory, epf_employee_voluntary,
			        epf_employer_statutory, epf_employer_voluntary, eps_employer,
			        esi_employee, esi_employer, tds, days_paid
			   FROM payslip WHERE pay_run_id = $1 AND employee_id = $2`,
			payRunID, employeeID).Scan(dest...); err != nil {
			return err
		}
		for i, c := range cols {
			out[c] = vals[i]
		}
		return nil
	}); err != nil {
		t.Fatalf("read persisted payslip for %s: %v", employeeID, err)
	}
	return out
}

func wantSlip(t *testing.T, who string, got map[string]decimal.Decimal, want map[string]string) {
	t.Helper()
	for col, w := range want {
		g, ok := got[col]
		if !ok {
			t.Errorf("%s: no column %q was read back", who, col)
			continue
		}
		if !g.Equal(dec(w)) {
			t.Errorf("%s: %s = %s, want %s (hand-derived from the statute — see the fixture comment)",
				who, col, g.StringFixed(4), w)
		}
	}
}

// TestComputeAndRunPayRunComputesTheMonthFromTheEngine is the end-to-end proof
// that internal/payroll is REACHED by the path that creates a payslip.
//
// Nothing in it supplies a rupee. It writes a salary structure and an election,
// asks for the month, and then checks the persisted payslips and the posted
// voucher against hand-derived statutory figures. Four properties would each
// fail it on their own:
//
//  1. THE ENGINE RAN. If ComputeAndRunPayRun had persisted the salary structure
//     verbatim, gross would be right and every other column would be zero.
//  2. THE Code on Social Security WAGE, not basic pay and not gross: 14,000 for
//     an 18,000 structure whose HRA is excluded.
//  3. THE ESI COVERAGE LATCH, over the FULL wage-revision history. Employee two
//     earns 25,000 in July, above the 21,000 coverage ceiling, and is covered
//     anyway because the latch was taken at 18,000 on 1 April. A monthly ceiling
//     test computes zero ESI for them and passes every other assertion here.
//  4. THE THREE PF WAGE BASES ARE SEPARATE NUMBERS carried to separate columns.
func TestComputeAndRunPayRunComputesTheMonthFromTheEngine(t *testing.T) {
	pool, ctx, _ := setup(t)
	svc := newService(pool)
	one, two := computeFixture(t, svc, ctx, "A")

	res, err := svc.ComputeAndRunPayRun(ctx, payrolldb.ComputePayRunInput{
		Month:     computeMonth,
		CreatedBy: "payroll-compute-test",
		Narration: "Payroll for " + computeMonth.String(),
	})
	if err != nil {
		t.Fatalf("compute and run pay run: %v", err)
	}
	if res.Status != payrolldb.StatusPosted {
		t.Fatalf("pay run status = %q, want POSTED", res.Status)
	}

	wantSlip(t, "employee one", persistedSlip(t, pool, ctx, res.PayRunID, one), map[string]string{
		"gross_earnings":         "18000",
		"statutory_wages":        "14000",
		"epf_wages":              "14000",
		"eps_wages":              "14000",
		"edli_wages":             "14000",
		"esi_wages":              "18000",
		"epf_employee_statutory": "1680",
		"epf_employer_statutory": "514",
		"eps_employer":           "1166",
		"esi_employee":           "135",
		"esi_employer":           "585",
		"tds":                    "0",
		"total_deductions":       "1815",
		"net_pay":                "16185",
		"days_paid":              "31",
	})

	wantSlip(t, "employee two (mid-period raise)", persistedSlip(t, pool, ctx, res.PayRunID, two), map[string]string{
		"gross_earnings":         "25000",
		"statutory_wages":        "20000",
		"epf_wages":              "15000",
		"eps_wages":              "15000",
		"edli_wages":             "15000",
		"esi_wages":              "25000",
		"epf_employee_statutory": "1800",
		"epf_employer_statutory": "550",
		"eps_employer":           "1250",
		"esi_employee":           "188",
		"esi_employer":           "813",
		"tds":                    "0",
		"total_deductions":       "1988",
		"net_pay":                "23012",
	})

	// THE LATCH, stated as its own assertion so the failure message says what
	// broke rather than leaving a reader to work it out from a wage column.
	if esi := persistedSlip(t, pool, ctx, res.PayRunID, two)["esi_employee"]; esi.IsZero() {
		t.Error("employee two contributed NO ESI in a month they earned 25,000, above the 21,000 coverage ceiling. " +
			"Coverage LATCHES at the start of the contribution period (they were on 18,000 on 1 April) and is held for the whole period; " +
			"a zero here means the ceiling was tested against THIS MONTH'S wage, which is a different rule that covers a different set of people")
	}

	// The posted voucher. Asserted on rows, like every other test in this
	// package: posting_balanced is a trigger on the posting TABLE, so a voucher
	// with no postings fires no balance check at all.
	ps := postingsOf(t, pool, ctx, res.VoucherID)
	if len(ps) == 0 {
		t.Fatal("the computed pay run posted NO posting rows at all")
	}
	debits, credits := decimal.Zero, decimal.Zero
	for _, v := range ps {
		debits = debits.Add(v[0])
		credits = credits.Add(v[1])
	}
	if !debits.Equal(credits) {
		t.Fatalf("the computed pay run does not balance: debits %s != credits %s", debits.StringFixed(4), credits.StringFixed(4))
	}
	for name, want := range map[string][2]string{
		payrolldb.LedgerSalariesAndWages: {"43000", "0"},
		payrolldb.LedgerEmployerPF:       {"3480", "0"},
		payrolldb.LedgerEmployerESI:      {"1398", "0"},
		payrolldb.LedgerSalaryPayable:    {"0", "39197"},
		payrolldb.LedgerPFPayable:        {"0", "6960"},
		payrolldb.LedgerESIPayable:       {"0", "1721"},
	} {
		got, ok := ps[name]
		if !ok {
			t.Errorf("no posting to %q; that head of the entry is missing entirely", name)
			continue
		}
		if !got[0].Equal(dec(want[0])) || !got[1].Equal(dec(want[1])) {
			t.Errorf("%s: debit %s / credit %s, want debit %s / credit %s",
				name, got[0].StringFixed(4), got[1].StringFixed(4), want[0], want[1])
		}
	}
	if _, ok := ps[payrolldb.LedgerTDSOnSalaryPayable]; ok {
		t.Error("a zero TDS leg was posted; a Trial Balance line for \"TDS on Salary Payable 0.00\" in a month nobody was taxed is a report that looks like a bug")
	}

	// THE DEFERRALS MUST REACH THE PAYSLIP. Three rates in the 2026 schemes are
	// expressly "to be notified separately" (the EDLI contribution, EDLI
	// administrative charges, EPF administrative charges) and the engine refuses
	// to invent any of them. A payslip that silently showed zero for an
	// unnotified component would be indistinguishable from one where the
	// component genuinely does not apply.
	view, err := svc.Payslip(ctx, computeMonth, one)
	if err != nil {
		t.Fatalf("payslip: %v", err)
	}
	if len(view.Deferrals) != 3 {
		t.Errorf("the computed payslip carries %d deferral(s), want 3 (EDLI contribution, EDLI admin charges, EPF admin charges): %+v",
			len(view.Deferrals), view.Deferrals)
	}
	// Five lines: three earnings, EPF employee, ESI employee. TDS is zero and a
	// zero line is omitted.
	if len(view.Lines) != 5 {
		t.Errorf("the computed payslip has %d lines, want 5 (BASIC, COMM, OTHR, EPF_EMPLOYEE, ESI_EMPLOYEE): %+v", len(view.Lines), view.Lines)
	}
}

// TestPayRunRefusesASlipTheEngineDidNotProduce is the guard on the hand-supplied
// path, and the tamper it uses is chosen to defeat the checks that were there
// before it.
//
// The slip it posts satisfies BOTH of SlipInput.validate's identities:
//
//	net  = gross - deductions    18,000 - 975 = 17,025    OK
//	deds = TDS + PF + ESI        0 + 840 + 135 = 975      OK
//
// It also balances as a voucher, posts cleanly, and reads back perfectly on the
// register. The ONLY thing wrong with it is that the employee's provident fund
// is half what the statute requires — which is a shortfall the employer owes
// with interest under s.14B and damages under s.14, and which nothing in this
// package could previously see.
func TestPayRunRefusesASlipTheEngineDidNotProduce(t *testing.T) {
	pool, ctx, _ := setup(t)
	svc := newService(pool)
	one, _ := computeFixture(t, svc, ctx, "B")

	// Take the ENGINE's own output and edit one figure. Starting from a
	// hand-written slip would leave it open whether the refusal came from the
	// tamper or from some unrelated difference.
	slips, inputs, err := svc.ComputeSlips(ctx, computeMonth, []payrolldb.MonthInput{{EmployeeID: one}})
	if err != nil {
		t.Fatalf("compute slips: %v", err)
	}
	if len(slips) != 1 {
		t.Fatalf("computed %d slips, want 1", len(slips))
	}
	if !slips[0].EPFEmployeeStatutory.Equal(dec("1680")) {
		t.Fatalf("the fixture no longer computes an employee EPF of 1,680 (got %s); the tamper below would not be a halving",
			slips[0].EPFEmployeeStatutory.StringFixed(4))
	}

	tampered := slips[0]
	tampered.EPFEmployeeStatutory = dec("840")
	tampered.TotalDeductions = dec("975")
	tampered.NetPay = dec("17025")
	// The component line is edited to agree, so the run is internally
	// consistent in every way this package used to check. The slice is COPIED
	// first: a struct copy shares the backing array, and editing it in place
	// would corrupt the untampered control run at the end of this test.
	tampered.Components = append([]payrolldb.ComponentLine(nil), slips[0].Components...)
	for i := range tampered.Components {
		if tampered.Components[i].Code == payroll.ComponentEPFEmployee {
			tampered.Components[i].Amount = dec("840")
		}
	}

	_, err = svc.RunPayRun(ctx, payrolldb.PayRunInput{
		Month:       computeMonth,
		CreatedBy:   "payroll-compute-test",
		Narration:   "Payroll for " + computeMonth.String(),
		Slips:       []payrolldb.SlipInput{tampered},
		MonthInputs: inputs,
	})
	if err == nil {
		t.Fatal("a pay run posted an employee's provident fund at HALF the statutory rate. " +
			"The slip satisfies both of validate()'s self-consistency identities and balances as a voucher, so nothing else in this package objects; " +
			"the shortfall is recoverable from the employer with interest under s.14B and damages under s.14, and it is invisible in the books")
	}
	if !errors.Is(err, payrolldb.ErrSlipNotFromEngine) {
		t.Errorf("the refusal is not ErrSlipNotFromEngine, so a caller cannot tell it apart from an ordinary failure: %v", err)
	}
	// The message has to name the field and BOTH figures, or it is unactionable
	// on a run of two hundred people.
	for _, want := range []string{"epf_employee_statutory", "840", "1680", one.String()} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q, so it does not say what is wrong or whose payslip it is: %v", want, err)
		}
	}

	// AND NOTHING WAS WRITTEN. The check runs before the draft, so a refused run
	// must leave no pay_run row, no payslip and no voucher — payslip rows are
	// immutable, and a half-written refused run could not be cleaned up.
	var runs, slipRows int
	if err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM pay_run`).Scan(&runs); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*) FROM payslip`).Scan(&slipRows)
	}); err != nil {
		t.Fatalf("count rows after the refusal: %v", err)
	}
	if runs != 0 || slipRows != 0 {
		t.Errorf("a refused pay run left %d pay_run row(s) and %d payslip row(s) behind; payslips are immutable and cannot be cleaned up", runs, slipRows)
	}

	// THE CONTROL. The untampered slips must still post, or the test above
	// would pass for a gate that refuses everything.
	if _, err := svc.RunPayRun(ctx, payrolldb.PayRunInput{
		Month:       computeMonth,
		CreatedBy:   "payroll-compute-test",
		Narration:   "Payroll for " + computeMonth.String(),
		Slips:       slips,
		MonthInputs: inputs,
	}); err != nil {
		t.Fatalf("the engine's OWN output was refused by the engine gate, which would make every pay run impossible: %v", err)
	}
}

// TestComputeRefusesAMidMonthRevisionRatherThanGuessing pins the refusal
// documented in compute.go.
//
// A structure that changes on the 15th has two available answers — pay the
// whole month at the old rate, or at the new one — and both are wrong without
// proration, which is not modelled. A refusal naming the employee and the date
// is recoverable; a silently halved (or doubled) month is somebody's salary.
func TestComputeRefusesAMidMonthRevisionRatherThanGuessing(t *testing.T) {
	pool, ctx, _ := setup(t)
	svc := newService(pool)
	emp := createEmployee(t, svc, ctx, "E-CMP-MID", "Mid Month Revision")
	setStructureRow(t, svc, ctx, emp, "2026-04-01", "2026-07-15", "BASIC", payroll.ComponentBasic, "12000")
	setStructureRow(t, svc, ctx, emp, "2026-07-15", "", "BASIC", payroll.ComponentBasic, "18000")
	setElection(t, svc, ctx, emp)

	_, _, err := svc.ComputeSlips(ctx, computeMonth, []payrolldb.MonthInput{{EmployeeID: emp}})
	if err == nil {
		t.Fatal("a salary revision dated the 15th was computed as a whole month at one rate or the other; " +
			"both answers are wrong without proration and neither is signalled anywhere")
	}
	for _, want := range []string{"2026-07-15", "proration"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}
}

// ---------------------------------------------------------------------------
// MID-MONTH JOINERS AND LEAVERS.
//
// The refusal above reads the effective-dated STRUCTURE and ELECTION windows.
// It never read the master's date of joining or date of leaving, and before
// refuseMidMonthEmployment existed an employee who LEFT ON THE 5TH of the wage
// month, whose salary structure was an ordinary open-ended row laid down months
// earlier, sailed past every check and was paid A WHOLE MONTH. The figures
// observed on the live database, for the 18,000 fixture structure, were:
//
//	gross 18,000   EPF employee 1,680   ESI employee 135   days paid 31
//
// i.e. a payslip stating a month that was not worked, an ECR and an ESI return
// over-reporting the wage (recoverable from the employer with interest and
// damages under s.85B, and never recoverable from the EPFO at all), and a s.392
// deduction on earnings that did not exist. The joiner is the same lie: an
// employee joining on the 20th got the full 18,000 whenever their structure
// predated them — the accidental cover from the structure-window refusal only
// applies when the structure happens to start on the joining date.
//
// The fix REFUSES rather than prorating, for the reason the file gives for
// mid-month revisions: proration is a policy choice (calendar days, working
// days, the 26-day month) that this file cannot make, and a short salary that
// ships silently is not recoverable.
// ---------------------------------------------------------------------------

// createEmployeeDated is createEmployee with the master's employment dates
// under the test's control. It is separate because the shared helper hard-codes
// a date of joining a year before every wage month these tests run — which is
// exactly the shape that hid this defect.
func createEmployeeDated(t *testing.T, svc *payrolldb.Service, ctx context.Context, code, name, doj, dol string) uuid.UUID {
	t.Helper()
	id, err := svc.CreateEmployee(ctx, payrolldb.Employee{
		Code: code, Name: name, DOJ: doj, DOL: dol,
		Designation: "Analyst", PAN: "ABCPD1234E", UAN: "100200300400",
		ESICIPNo: "1234567890", BankIFSC: "HDFC0001234", BankAccountLast4: "4321",
		IsActive: true,
	})
	if err != nil {
		t.Fatalf("create employee %s: %v", code, err)
	}
	return id
}

// datedFixtureEmployee creates one employee with the stated employment dates and
// the flat 18,000 structure, laid down from 1 April 2026 — BEFORE the wage month
// and before any of these joining dates, so that nothing but the master's dates
// can be what the run reacts to.
func datedFixtureEmployee(t *testing.T, svc *payrolldb.Service, ctx context.Context, code, name, doj, dol string) uuid.UUID {
	t.Helper()
	id := createEmployeeDated(t, svc, ctx, code, name, doj, dol)
	flatStructure(t, svc, ctx, id)
	setElection(t, svc, ctx, id)
	return id
}

func TestComputeRefusesAMidMonthJoinerOrLeaverRatherThanPayingAWholeMonth(t *testing.T) {
	pool, ctx, _ := setup(t)
	svc := newService(pool)
	_ = pool

	cases := []struct {
		name     string
		code     string
		doj, dol string
		wantDate string
		// consequence states, in rupees, what a whole month paid to this person
		// actually costs — so a failure reads as the defect and not as a
		// disagreement about a boolean.
		consequence string
	}{
		{
			name: "left on the fifth", code: "E-CMP-LEAVE-5",
			doj: "2025-06-01", dol: "2026-07-05", wantDate: "2026-07-05",
			consequence: "an employee whose last working day was 5 July was paid the WHOLE of July: " +
				"gross 18,000 for five days worked, EPF 1,680 and ESI 135 withheld on a month that was not earned, " +
				"'31 days paid' on the ESI return and a full month's wage on the ECR",
		},
		{
			name: "left on the first", code: "E-CMP-LEAVE-1",
			doj: "2025-06-01", dol: "2026-07-01", wantDate: "2026-07-01",
			consequence: "an employee whose last working day was 1 July was paid the WHOLE of July — " +
				"18,000 gross for one day worked",
		},
		{
			name: "joined on the twentieth", code: "E-CMP-JOIN-20",
			doj: "2026-07-20", dol: "", wantDate: "2026-07-20",
			consequence: "an employee who joined on 20 July was paid the WHOLE of July: " +
				"gross 18,000 for twelve days worked, with EPF 1,680 and ESI 135 contributed on wages nobody earned",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			emp := datedFixtureEmployee(t, svc, ctx, c.code, c.name, c.doj, c.dol)
			slips, _, err := svc.ComputeSlips(ctx, computeMonth, []payrolldb.MonthInput{{EmployeeID: emp}})
			if err == nil {
				var got string
				for _, s := range slips {
					got = fmt.Sprintf("gross=%s net=%s epfEmployee=%s esiEmployee=%s daysPaid=%s",
						s.GrossEarnings, s.NetPay, s.EPFEmployeeStatutory, s.ESIEmployee, s.DaysPaid)
				}
				t.Fatalf("the run was NOT refused: %s. %s. Proration is not modelled here, so the only "+
					"answer that is not a wrong salary is a refusal naming the employee and the date", c.consequence, got)
			}
			for _, want := range []string{c.code, c.wantDate, "inside the wage month", "proration"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not mention %q, so the payroll clerk cannot tell WHO to pay "+
						"outside the run or for WHAT part month: %v", want, err)
				}
			}
		})
	}
}

// TestComputeStillPaysAWholeMonthAtTheEmploymentBOUNDARIES is the negative
// control on the refusal above, and it is not optional: a guard written with
// either boundary one day out refuses every ordinary joiner and leaver — the
// employer's whole payroll — and the refusal test above passes just the same.
//
// Joining ON the first day of the wage month IS a whole month. Leaving ON the
// last day IS a whole month, because a date of leaving is the LAST WORKING DAY
// and not the day after it.
func TestComputeStillPaysAWholeMonthAtTheEmploymentBoundaries(t *testing.T) {
	pool, ctx, _ := setup(t)
	svc := newService(pool)
	_ = pool

	cases := []struct {
		name     string
		code     string
		doj, dol string
		why      string
	}{
		{
			name: "joined on the first of the wage month", code: "E-CMP-JOIN-1",
			doj: "2026-07-01", dol: "",
			why: "somebody who started on 1 July worked all of July and is owed all 18,000 of it",
		},
		{
			name: "left on the last day of the wage month", code: "E-CMP-LEAVE-31",
			doj: "2025-06-01", dol: "2026-07-31",
			why: "a date of leaving is the LAST WORKING DAY, so 31 July is a full month worked and the " +
				"final salary of 18,000 is due in this run — refusing it withholds a leaver's last pay entirely",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			emp := datedFixtureEmployee(t, svc, ctx, c.code, c.name, c.doj, c.dol)
			slips, _, err := svc.ComputeSlips(ctx, computeMonth, []payrolldb.MonthInput{{EmployeeID: emp}})
			if err != nil {
				t.Fatalf("a whole month was refused: %s. %v", c.why, err)
			}
			if len(slips) != 1 {
				t.Fatalf("want one payslip, got %d", len(slips))
			}
			if got := slips[0].GrossEarnings; !got.Equal(dec("18000")) {
				t.Errorf("gross earnings = %s, want 18000: %s", got.StringFixed(2), c.why)
			}
			if got := slips[0].NetPay; !got.Equal(dec("16185")) {
				t.Errorf("net pay = %s, want 16185 (18,000 less EPF 1,680 and ESI 135): %s", got.StringFixed(2), c.why)
			}
		})
	}
}
