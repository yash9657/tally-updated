package payrolldb_test

// payrun_test.go — the posting seam.
//
// Every assertion in this file is on POSTED ROWS, not on a nil error. That is
// deliberate and it is the single most important property of this file:
// posting_balanced (migration 0002) is a CONSTRAINT TRIGGER on the posting
// TABLE, so a voucher that inserts ZERO posting rows fires NO balance check at
// all and PostVoucher returns nil for it. "The post succeeded" and "the pay run
// posted nothing whatsoever" are indistinguishable from the return value.

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/payroll"
	"github.com/khata/cored/internal/payroll/payrolldb"
	"github.com/khata/cored/internal/platform/db"
)

// wageMonth is the month every test in this file runs. July 2026 is chosen
// because it is inside BOTH shipped rulesets' coverage (the EPF/EPS/EDLI
// Schemes 2026 take effect 1 July 2026 and tax year 2026-27 runs Apr-Mar), so a
// resolution failure here is a real gap and not a fixture that wandered out of
// the table.
var wageMonth = payroll.WageMonth{Year: 2026, Month: 7}

// ---------------------------------------------------------------------------
// Fixtures.
//
// ===========================================================================
// THE SLIPS COME FROM THE ENGINE. THEY USED TO BE HAND-WRITTEN, AND COULD NOT.
// ===========================================================================
// Until CreateDraftPayRun grew its engine gate (ErrSlipNotFromEngine) these
// fixtures hand-derived slipA and slipB with figures a human wrote down, on the
// stated reasoning that the posting seam should not be fed engine output. That
// reasoning is now unavailable: the write path RE-DERIVES every slip from the
// database and refuses anything that is not internal/payroll's, so a
// hand-written fixture is refused before it reaches a single assertion — which
// is exactly what happened, and it left voucher balance, crash idempotency,
// payslip immutability and tenant isolation verified by nothing at all.
//
// So samplePayRun asks svc.ComputeSlips for the month and hands the result
// straight to RunPayRun, unedited. The property this file tests is unchanged
// and is NOT the engine's arithmetic: it is that those figures reach the right
// LEDGER on the right SIDE, that the voucher is real, and that the seam
// recovers from a crash. Every rupee asserted below is derived from the statute
// in the comment above the assertion, so a wrong engine still fails here.
//
// ---------------------------------------------------------------------------
// THE TWO STRUCTURES, AND THE HAND DERIVATION OF EVERY FIGURE THEY PRODUCE
// ---------------------------------------------------------------------------
// Wage month July 2026 -> ss-2026-07 (EPF 12%, EPS 8.33%, all three wage
// ceilings 15,000; ESI 0.75/3.25, coverage ceiling 21,000) and it-ty-2026-27
// (new-regime slabs, standard deduction 75,000, s.156 rebate limit 12,00,000).
// Both employees elect PFBaseCeiling, EPS membership, ESI applicability and the
// s.202 (new) regime, and hold a PAN.
//
// EMPLOYEE A — wageAboveESICeiling, a single BASIC of 2,00,000 from 1 Apr 2026.
//
//	s.2(88): BASIC is not on the exclusion list      STATUTORY WAGE  2,00,000
//	EPF base CEILING: min(2,00,000, 15,000)                            15,000
//	  employee  12% x 15,000                                            1,800
//	  EPS       8.33% x 15,000 = 1,249.50 -> 1,250 (half away from zero)
//	  employer EPF = 1,800 - 1,250                                        550
//	ESI: the coverage latch is taken on 1 Apr 2026 (the start of the
//	  Apr-Sep contribution period) at 2,00,000, which EXCEEDS the 21,000
//	  ceiling -> NOT COVERED. Both shares are zero and, because a zero leg is
//	  omitted, this employee posts no ESI line at all.
//	s.392: the employee has no April-June payslips in a throwaway company, so
//	  the annual estimate is this month plus the eight after it:
//	    salary            2,00,000 x 9                            = 18,00,000
//	    less s.19 standard deduction (new regime)                      75,000
//	    total income (s.516 rounds to the nearest TEN: already flat) 17,25,000
//	    slabs: 5% x 4,00,000 + 10% x 4,00,000 + 15% x 4,00,000
//	           + 20% x 1,25,000                                     = 1,45,000
//	    no s.156 rebate (income exceeds 12,00,000), no surcharge below 50 lakh
//	    cess 4% x 1,45,000 = 5,800                    annual tax    = 1,50,800
//	    spread over the 9 months remaining inclusive:
//	           1,50,800 / 9 = 16,755.5555... -> 16,756 (s.516, nearest rupee)
//	  deductions 1,800 + 0 + 16,756 = 18,556          NET  1,81,444
//
// EMPLOYEE B — wageWithinESICeiling, a single BASIC of 18,000. It exists so the
// ESI legs are exercised rather than optimised away as zero: a test whose only
// employee is above every ceiling proves nothing about the ESI ledgers.
//
//	STATUTORY WAGE 18,000; EPF base min(18,000, 15,000) = 15,000, so the three
//	provident-fund figures are the SAME as employee A's (1,800 / 1,250 / 550).
//	ESI: latched at 18,000 <= 21,000 -> COVERED for the whole period.
//	  employee  0.75% x 18,000 = 135    -> 135 (Rule 51 rounds UP)
//	  employer  3.25% x 18,000 = 585    -> 585
//	s.392: 18,000 x 9 = 1,62,000, less 75,000 = 87,000, below the 4,00,000 nil
//	  slab -> TDS 0, so this employee posts no TDS line.
//	  deductions 1,800 + 135 = 1,935                  NET     16,065
//
// ---------------------------------------------------------------------------
// THE VOUCHER FOR A RUN OF BOTH
//
//	Dr Salaries & Wages           2,00,000 + 18,000        = 2,18,000
//	Dr Employer PF Contribution   (550 + 1,250) x 2        =    3,600
//	Dr Employer ESI Contribution  0 + 585                  =      585
//	  Cr Salary Payable           1,81,444 + 16,065        = 1,97,509
//	  Cr TDS on Salary Payable    16,756 + 0               =   16,756
//	  Cr PF Payable               (1,800 + 550 + 1,250) x 2=    7,200
//	  Cr ESI Payable              0 + (135 + 585)          =      720
//	                                        2,22,185 = 2,22,185
//
// The three EPFO CHARGES (the EDLI premium and the two administrative charges)
// are DEFERRED rather than computed — the 2026 Schemes say each rate is to be
// notified separately — so they carry no amount into the employer's debit here.
// Their absence is asserted as three deferrals on the payslip, not as a zero.
// ---------------------------------------------------------------------------

const (
	// wageAboveESICeiling is employee A's monthly BASIC. It is chosen ABOVE the
	// 21,000 ESI coverage ceiling and high enough to carry a real s.392
	// liability, so this file exercises the TDS leg — the whole class of
	// employees for whom the withholding is the largest credit on the entry.
	wageAboveESICeiling = "200000"
	// wageWithinESICeiling is employee B's monthly BASIC, inside the coverage
	// ceiling so both ESI legs are non-zero.
	wageWithinESICeiling = "18000"
)

// samplePayRun asks the ENGINE for the wage month's payslips for the named
// employees and returns them, unedited, as a PayRunInput.
//
// The MonthInputs ComputeSlips resolved travel with it. That is not optional
// bookkeeping: the write path re-derives every slip, and a run that discarded
// the inputs would be re-derived against different attendance and refused.
func samplePayRun(t *testing.T, svc *payrolldb.Service, ctx context.Context, employees ...uuid.UUID) payrolldb.PayRunInput {
	t.Helper()
	return samplePayRunFor(t, svc, ctx, wageMonth, employees...)
}

// samplePayRunFor is samplePayRun for a month other than the file's default. It
// exists for the crash-recovery test, which needs a SECOND month: the
// one-live-run-per-month index would otherwise refuse the draft, and the s.392
// spread is a function of the months remaining, so July's slips are genuinely
// not August's.
func samplePayRunFor(t *testing.T, svc *payrolldb.Service, ctx context.Context,
	month payroll.WageMonth, employees ...uuid.UUID) payrolldb.PayRunInput {
	t.Helper()
	// The selection is EXPLICIT. An empty Inputs means "every eligible
	// employee", which would silently pull in the other employees a test
	// created for a different purpose.
	sel := make([]payrolldb.MonthInput, 0, len(employees))
	for _, id := range employees {
		sel = append(sel, payrolldb.MonthInput{EmployeeID: id})
	}
	slips, used, err := svc.ComputeSlips(ctx, month, sel)
	if err != nil {
		t.Fatalf("compute the %s payslips for the fixture: %v", month, err)
	}
	if len(slips) != len(employees) {
		t.Fatalf("the engine produced %d payslip(s) for %d employee(s) in %s", len(slips), len(employees), month)
	}
	return payrolldb.PayRunInput{
		Month:       month,
		CreatedBy:   "payroll-test",
		Narration:   "Payroll for " + month.String(),
		Slips:       slips,
		MonthInputs: used,
	}
}

func createEmployee(t *testing.T, svc *payrolldb.Service, ctx context.Context, code, name string) uuid.UUID {
	t.Helper()
	id, err := svc.CreateEmployee(ctx, payrolldb.Employee{
		Code: code, Name: name, DOJ: "2025-06-01",
		Designation: "Analyst", PAN: "ABCPD1234E", UAN: "100200300400",
		ESICIPNo: "1234567890", BankIFSC: "HDFC0001234", BankAccountLast4: "4321",
		IsActive: true,
	})
	if err != nil {
		t.Fatalf("create employee %s: %v", code, err)
	}
	return id
}

// setStructureAndElection lays down employee A's structure — the one above the
// ESI coverage ceiling and carrying a real s.392 liability. It is the default
// because most tests in this file run ONE employee and want the TDS leg.
func setStructureAndElection(t *testing.T, svc *payrolldb.Service, ctx context.Context, empID uuid.UUID, from string) {
	t.Helper()
	setStructureAndElectionAt(t, svc, ctx, empID, from, wageAboveESICeiling)
}

// setStructureAndElectionAt is setStructureAndElection at a stated monthly
// BASIC, so a test can put a second employee INSIDE the ESI coverage ceiling.
func setStructureAndElectionAt(t *testing.T, svc *payrolldb.Service, ctx context.Context, empID uuid.UUID, from, basic string) {
	t.Helper()
	if _, err := svc.SetSalaryComponent(ctx, payrolldb.SalaryComponent{
		EmployeeID: empID, EffectiveFrom: from,
		Code: "BASIC", Type: payroll.ComponentBasic, Amount: dec(basic),
	}); err != nil {
		t.Fatalf("set salary component: %v", err)
	}
	if _, err := svc.SetStatutoryElection(ctx, payrolldb.StatutoryElection{
		EmployeeID: empID, EffectiveFrom: from,
		PFBase: payroll.PFBaseCeiling, EPSMember: true, ESIApplicable: true,
		Regime: payroll.RegimeNew,
	}); err != nil {
		t.Fatalf("set statutory election: %v", err)
	}
}

// postingsOf reads the CURRENT version's postings for a voucher, as ledger
// name -> (debit, credit). Reading through voucher_head means an ALTER is
// observed as the restated lines rather than the superseded ones.
func postingsOf(t *testing.T, pool *db.Pool, ctx context.Context, voucherID uuid.UUID) map[string][2]decimal.Decimal {
	t.Helper()
	out := map[string][2]decimal.Decimal{}
	if err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT l.name, p.dr_cr, p.amount
			   FROM voucher_head vh
			   JOIN posting p ON p.voucher_version_id = vh.current_version_id
			   JOIN ledger l ON l.id = p.ledger_id
			  WHERE vh.voucher_id = $1`, voucherID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var name, drcr string
			var amt decimal.Decimal
			if err := rows.Scan(&name, &drcr, &amt); err != nil {
				return err
			}
			cur := out[name]
			if drcr == "D" {
				cur[0] = cur[0].Add(amt)
			} else {
				cur[1] = cur[1].Add(amt)
			}
			out[name] = cur
		}
		return rows.Err()
	}); err != nil {
		t.Fatalf("read postings: %v", err)
	}
	return out
}

// TestPayRunPostsThroughTheLedgerAndBalances asserts on the POSTED ROWS.
//
// It checks four things, in order of what would go wrong:
//
//  1. THAT ANY POSTINGS EXIST AT ALL. posting_balanced is a trigger on the
//     posting table, so a pay run that wrote no postings would commit silently,
//     leave the trial balance balanced (and missing the payroll), and return
//     nil. This assertion is the only thing standing between that failure and
//     nobody noticing.
//  2. That debits equal credits across the real rows.
//  3. That each control ledger carries the amount the entry says it should, so
//     a leg on the wrong SIDE (which still balances if two are swapped) or
//     posted to the wrong ledger is caught.
//  4. That the employer's contribution appears as BOTH an expense debit and a
//     liability credit, and that the employee's share appears ONLY as a
//     liability — booking the employee share as an expense would overstate the
//     wage bill by 12% of the PF wage in every company that used it.
func TestPayRunPostsThroughTheLedgerAndBalances(t *testing.T) {
	pool, ctx, _ := setup(t)
	svc := newService(pool)

	empA := createEmployee(t, svc, ctx, "E-1", "Employee One")
	empB := createEmployee(t, svc, ctx, "E-2", "Employee Two")
	setStructureAndElection(t, svc, ctx, empA, "2026-04-01")
	setStructureAndElectionAt(t, svc, ctx, empB, "2026-04-01", wageWithinESICeiling)

	res, err := svc.RunPayRun(ctx, samplePayRun(t, svc, ctx, empA, empB))
	if err != nil {
		t.Fatalf("run pay run: %v", err)
	}
	if res.Status != payrolldb.StatusPosted {
		t.Fatalf("pay run status = %q, want POSTED", res.Status)
	}
	if res.VoucherNo == "" {
		t.Error("the pay run's voucher has no number; numbering did not run, which is the visible half of a NULL voucher_type_id")
	}

	ps := postingsOf(t, pool, ctx, res.VoucherID)
	if len(ps) == 0 {
		t.Fatal("the pay run posted NO posting rows at all. posting_balanced is a constraint trigger on the posting TABLE, " +
			"so a voucher with zero postings fires no balance check and commits silently: the salaries, the TDS and the PF/ESI " +
			"liabilities would all be missing from the books while the trial balance still balanced")
	}

	debits, credits := decimal.Zero, decimal.Zero
	for _, v := range ps {
		debits = debits.Add(v[0])
		credits = credits.Add(v[1])
	}
	if !debits.Equal(credits) {
		t.Fatalf("the pay run does not balance: debits %s != credits %s", debits.StringFixed(4), credits.StringFixed(4))
	}
	// Two employees: 2,00,000 + 18,000 gross, employer PF 1,800 each (550 EPF +
	// 1,250 EPS), employer ESI 585 for the second only — the first is above the
	// coverage ceiling. See the fixture header for the derivation of each.
	if want := dec("222185"); !debits.Equal(want) {
		t.Errorf("total debits = %s, want %s (2,18,000 gross + 3,600 employer PF + 585 employer ESI)",
			debits.StringFixed(4), want.StringFixed(4))
	}

	expect := map[string][2]string{
		payrolldb.LedgerSalariesAndWages: {"218000", "0"},
		payrolldb.LedgerEmployerPF:       {"3600", "0"},
		payrolldb.LedgerEmployerESI:      {"585", "0"},
		payrolldb.LedgerSalaryPayable:    {"0", "197509"},
		// 16,756 is employee A's s.392 deduction for July and employee B owes
		// nothing: 1,50,800 of annual tax spread over the nine months remaining
		// inclusive. It is the largest credit on the entry after net pay, and
		// this is the only assertion in the package that it reaches the ledger
		// the return is filed from rather than being netted into Salary Payable.
		payrolldb.LedgerTDSOnSalaryPayable: {"0", "16756"},
		payrolldb.LedgerPFPayable:          {"0", "7200"},
		payrolldb.LedgerESIPayable:         {"0", "720"},
	}
	for name, want := range expect {
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

	// (4) The employee's PF share must NOT appear as an expense. Employer PF
	// expense is 3,600 (2 x 1,800); PF Payable is 7,200 (employee 3,600 +
	// employer 3,600). If the employee share had been debited to expense the
	// first figure would be 7,200 and the wage bill would be overstated.
	if got := ps[payrolldb.LedgerEmployerPF][0]; !got.Equal(dec("3600")) {
		t.Errorf("Employer PF Contribution debited %s; the EMPLOYEE's share is not an employer cost — it is part of gross pay that is withheld, "+
			"and debiting it would overstate the wage bill by 12%% of the PF wage", got.StringFixed(4))
	}
}

// TestPayRunVoucherTypeIDIsNotNull pins the parent_class spelling trap in
// migration 0034's voucher_type seed.
//
// ledger.defaultVoucherTypeID matches
//
//	parent_class = upper(replace($class, ' ', '_')) AND is_system = true
//
// and returns uuid.Nil WITH NO ERROR on a miss. So a seed that wrote 'Payroll'
// instead of 'PAYROLL' produces a pay run that posts, balances, appears in the
// Trial Balance and the Day Book, and carries voucher_type_id NULL — which
// eliminates it from every report that reaches the class through a JOIN on
// voucher_type. That is the exact NULL that dropped altered invoices out of
// GSTR-1 and GSTR-3B, and nothing else in the system complains about it.
//
// This test exists solely to fail for that one mutation while every other
// payroll test still passes.
func TestPayRunVoucherTypeIDIsNotNull(t *testing.T) {
	pool, ctx, _ := setup(t)
	svc := newService(pool)

	emp := createEmployee(t, svc, ctx, "E-VT-1", "Voucher Type")
	setStructureAndElection(t, svc, ctx, emp, "2026-04-01")
	res, err := svc.RunPayRun(ctx, samplePayRun(t, svc, ctx, emp))
	if err != nil {
		t.Fatalf("run pay run: %v", err)
	}

	var typeID *uuid.UUID
	var typeName, parentClass *string
	if err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT vv.voucher_type_id, vt.name, vt.parent_class
			   FROM voucher_head vh
			   JOIN voucher_version vv ON vv.id = vh.current_version_id
			   LEFT JOIN voucher_type vt ON vt.id = vv.voucher_type_id
			  WHERE vh.voucher_id = $1`, res.VoucherID).
			Scan(&typeID, &typeName, &parentClass)
	}); err != nil {
		t.Fatalf("read voucher type: %v", err)
	}
	if typeID == nil {
		t.Fatal("voucher_version.voucher_type_id is NULL for the pay run. ledger.defaultVoucherTypeID returns uuid.Nil with NO ERROR when " +
			"no system voucher_type matches parent_class = upper(replace('Payroll',' ','_')) = 'PAYROLL', so the post succeeds and the voucher " +
			"is silently invisible to every report that joins voucher_type — the exact NULL that dropped altered invoices out of GSTR-1")
	}
	if typeName == nil || *typeName != "Payroll" {
		t.Errorf("the pay run resolved voucher type %v, want \"Payroll\"", typeName)
	}
	if parentClass == nil || *parentClass != "PAYROLL" {
		t.Errorf("voucher_type.parent_class = %v, want \"PAYROLL\" (the form defaultVoucherTypeID matches)", parentClass)
	}
}

// TestPayRunIsIdempotentAcrossACrashBetweenTheTwoTransactions reproduces BOTH
// crash windows of the write-draft-then-post seam and proves each recovers to
// exactly ONE voucher version.
//
// Window 1 — the post committed, the linkage UPDATE did not. Simulated by
// posting normally and then putting the pay_run row back to DRAFT, which is
// byte-for-byte the state the process would have died in. A resume must NOT
// post again: a second post would append version 2 for the same month's wages,
// doubling the entry in ledger_balance.
//
// Window 2 — the draft committed, the post did not. Reached honestly by calling
// CreateDraftPayRun and stopping there. A resume must post, under the SAME
// voucher id the draft already named.
func TestPayRunIsIdempotentAcrossACrashBetweenTheTwoTransactions(t *testing.T) {
	pool, ctx, _ := setup(t)
	svc := newService(pool)

	emp := createEmployee(t, svc, ctx, "E-CRASH-1", "Crash One")
	setStructureAndElection(t, svc, ctx, emp, "2026-04-01")

	// --- Window 1 -----------------------------------------------------------
	res, err := svc.RunPayRun(ctx, samplePayRun(t, svc, ctx, emp))
	if err != nil {
		t.Fatalf("run pay run: %v", err)
	}
	if err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, uerr := tx.Exec(ctx,
			`UPDATE pay_run SET status = 'DRAFT', posted_at = NULL WHERE id = $1`, res.PayRunID)
		return uerr
	}); err != nil {
		t.Fatalf("simulate the crash between the two transactions: %v", err)
	}

	again, err := svc.ResumePayRun(ctx, res.PayRunID, "payroll-test", "Payroll for "+wageMonth.String())
	if err != nil {
		t.Fatalf("resume after crash window 1: %v", err)
	}
	if again.VoucherID != res.VoucherID {
		t.Errorf("the resume used voucher %s, want the id the draft already named (%s)", again.VoucherID, res.VoucherID)
	}
	if n := versionCount(t, pool, ctx, res.VoucherID); n != 1 {
		t.Fatalf("after resuming a crashed pay run there are %d voucher versions, want exactly 1; "+
			"a second post appends another version of the SAME month's wages and doubles the entry in ledger_balance", n)
	}
	if again.Status != payrolldb.StatusPosted {
		t.Errorf("status after resume = %q, want POSTED", again.Status)
	}

	// A third call must be a pure no-op through the replay guard.
	third, err := svc.ResumePayRun(ctx, res.PayRunID, "payroll-test", "")
	if err != nil {
		t.Fatalf("third resume: %v", err)
	}
	if !third.AlreadyPosted {
		t.Error("the replay guard did not fire on an already-POSTED run")
	}
	if n := versionCount(t, pool, ctx, res.VoucherID); n != 1 {
		t.Fatalf("a replayed resume produced %d voucher versions, want 1", n)
	}

	// --- Window 2 -----------------------------------------------------------
	empB := createEmployee(t, svc, ctx, "E-CRASH-2", "Crash Two")
	setStructureAndElection(t, svc, ctx, empB, "2026-04-01")
	// AUGUST, and computed FOR August rather than July's slips relabelled. The
	// s.392 deduction is the year's remaining tax divided by the months
	// remaining INCLUSIVE, so July's figure is genuinely not August's, and the
	// engine gate re-derives against the month on the input — a relabelled July
	// run is refused before it can reach the crash window this test exists for.
	augustMonth := payroll.WageMonth{Year: 2026, Month: 8}
	draftIn := samplePayRunFor(t, svc, ctx, augustMonth, empB)
	draftID, err := svc.CreateDraftPayRun(ctx, draftIn)
	if err != nil {
		t.Fatalf("create draft: %v", err)
	}
	var draftVoucher uuid.UUID
	if err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT voucher_id FROM pay_run WHERE id = $1`, draftID).Scan(&draftVoucher)
	}); err != nil {
		t.Fatalf("read draft voucher id: %v", err)
	}
	if n := versionCount(t, pool, ctx, draftVoucher); n != 0 {
		t.Fatalf("a DRAFT pay run already has %d voucher versions; the draft must not post", n)
	}
	out, err := svc.ResumePayRun(ctx, draftID, "payroll-test", "recovered")
	if err != nil {
		t.Fatalf("resume after crash window 2: %v", err)
	}
	if out.VoucherID != draftVoucher {
		t.Errorf("the recovery posted under voucher %s, want the id the draft named (%s); "+
			"posting under a fresh id would leave the draft pointing at a voucher that never existed", out.VoucherID, draftVoucher)
	}
	if n := versionCount(t, pool, ctx, draftVoucher); n != 1 {
		t.Fatalf("after recovering a never-posted draft there are %d voucher versions, want 1", n)
	}
}

func versionCount(t *testing.T, pool *db.Pool, ctx context.Context, voucherID uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM voucher_version WHERE voucher_id = $1`, voucherID).Scan(&n)
	}); err != nil {
		t.Fatalf("count versions: %v", err)
	}
	return n
}

// TestPayRunIsAlterableAndReversible proves the pay run's voucher behaves like
// any other voucher, because nothing in this package goes around
// ledger.post(): an alter appends a version onto the same hash chain, and a
// reversal tombstones the head and frees the month for a corrected run.
func TestPayRunIsAlterableAndReversible(t *testing.T) {
	pool, ctx, _ := setup(t)
	svc := newService(pool)

	emp := createEmployee(t, svc, ctx, "E-ALT-1", "Alterable")
	setStructureAndElection(t, svc, ctx, emp, "2026-04-01")
	res, err := svc.RunPayRun(ctx, samplePayRun(t, svc, ctx, emp))
	if err != nil {
		t.Fatalf("run pay run: %v", err)
	}

	altered, err := svc.AlterPayRunVoucher(ctx, res.PayRunID, "Payroll for July 2026 (corrected narration)", "payroll-test")
	if err != nil {
		t.Fatalf("alter pay run voucher: %v", err)
	}
	if altered.VersionNo != 2 {
		t.Errorf("after an alter the current version is %d, want 2", altered.VersionNo)
	}
	// The restated version must carry the SAME legs — buildPostings is
	// single-sourced precisely so a restated voucher cannot drift from the one
	// it supersedes.
	after := postingsOf(t, pool, ctx, res.VoucherID)
	// 2,00,000 is the single employee's gross — their whole BASIC. See the
	// fixture header.
	if got := after[payrolldb.LedgerSalariesAndWages][0]; !got.Equal(dec(wageAboveESICeiling)) {
		t.Errorf("after the alter, Salaries & Wages is debited %s, want %s — the restated version dropped or changed a leg",
			got.StringFixed(4), wageAboveESICeiling)
	}
	// And the voucher_type_id must survive the alter. ledger.post carries it
	// forward from the superseded version; a NULL here is the GSTR-1 bug again.
	var typeID *uuid.UUID
	if err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT vv.voucher_type_id FROM voucher_head vh
			   JOIN voucher_version vv ON vv.id = vh.current_version_id
			  WHERE vh.voucher_id = $1`, res.VoucherID).Scan(&typeID)
	}); err != nil {
		t.Fatalf("read altered voucher type: %v", err)
	}
	if typeID == nil {
		t.Error("the ALTERED pay run has voucher_type_id NULL; the carry-forward in ledger.post did not run for this class")
	}

	// Reverse.
	if err := svc.ReversePayRun(ctx, res.PayRunID, "payroll-test"); err != nil {
		t.Fatalf("reverse pay run: %v", err)
	}
	var status string
	var deleted bool
	if err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT status FROM pay_run WHERE id = $1`, res.PayRunID).Scan(&status); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT is_deleted FROM voucher_head WHERE voucher_id = $1`, res.VoucherID).Scan(&deleted)
	}); err != nil {
		t.Fatalf("read reversed state: %v", err)
	}
	if status != payrolldb.StatusReversed {
		t.Errorf("pay run status after reversal = %q, want REVERSED", status)
	}
	if !deleted {
		t.Error("the pay run's voucher was not tombstoned; the wage bill is still in the books after a reversal")
	}

	// THE POINT OF REVERSING: the month is free to be re-run. The one-live-run
	// -per-month unique index is partial on status <> 'REVERSED' for exactly
	// this reason — a plain UNIQUE would make correcting a wrong pay run
	// impossible without deleting the audit trail.
	if _, err := svc.RunPayRun(ctx, samplePayRun(t, svc, ctx, emp)); err != nil {
		t.Fatalf("re-running the month after a reversal was refused: %v", err)
	}

	// And the payslips of the reversed run SURVIVE. They are immutable, and a
	// reversal is meant to leave the wrong figure and the right one both
	// visible; deleting them would make a correction indistinguishable from a
	// month that never ran.
	var slips int
	if err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM payslip WHERE pay_run_id = $1`, res.PayRunID).Scan(&slips)
	}); err != nil {
		t.Fatalf("count reversed payslips: %v", err)
	}
	if slips == 0 {
		t.Error("the reversed run's payslips are gone; the audit trail of what was originally computed has been destroyed")
	}
}

// TestPayslipIsImmutable proves both UPDATE and DELETE raise, on both tables.
//
// A payslip is what an employee was told they were paid and what a statutory
// return was filed from. An UPDATE would leave a filed return pointing at a
// number that no longer exists anywhere; the remedy for a wrong pay run is to
// reverse it and re-run the month, which leaves both figures visible.
func TestPayslipIsImmutable(t *testing.T) {
	pool, ctx, _ := setup(t)
	svc := newService(pool)

	emp := createEmployee(t, svc, ctx, "E-IMM-1", "Immutable")
	setStructureAndElection(t, svc, ctx, emp, "2026-04-01")
	res, err := svc.RunPayRun(ctx, samplePayRun(t, svc, ctx, emp))
	if err != nil {
		t.Fatalf("run pay run: %v", err)
	}

	cases := []struct {
		name, sql, want string
	}{
		{"payslip UPDATE", `UPDATE payslip SET net_pay = net_pay + 1 WHERE pay_run_id = $1`, "payslip is immutable"},
		{"payslip DELETE", `DELETE FROM payslip WHERE pay_run_id = $1`, "payslip is immutable"},
		{"payslip_component UPDATE", `UPDATE payslip_component SET amount = amount + 1
		   WHERE payslip_id IN (SELECT id FROM payslip WHERE pay_run_id = $1)`, "payslip_component is immutable"},
		{"payslip_component DELETE", `DELETE FROM payslip_component
		   WHERE payslip_id IN (SELECT id FROM payslip WHERE pay_run_id = $1)`, "payslip_component is immutable"},
	}
	for _, c := range cases {
		err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			_, e := tx.Exec(ctx, c.sql, res.PayRunID)
			return e
		})
		if err == nil {
			t.Errorf("%s succeeded; a payslip is what an employee was told they were paid and what a return was filed from, and it must not be editable", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s failed with %q, want a message containing %q", c.name, err, c.want)
		}
	}
}

// TestPerEmployeePayableLedgerSplitsTheNetPayLeg proves the OPT-IN half of the
// employee/ledger decision: an employee with payable_ledger_id set has their
// net pay credited to their own ledger, and everybody else lands in the
// aggregate control ledger.
//
// The default matters more than the option. An employee-named ledger appears in
// the Day Book, the Trial Balance and the Balance Sheet, none of which have
// per-ledger access control and all of which the seeded Data Entry level can
// read — so per-employee outstandings must be something a company opts into,
// never the shape everybody gets.
func TestPerEmployeePayableLedgerSplitsTheNetPayLeg(t *testing.T) {
	pool, ctx, _ := setup(t)
	svc := newService(pool)
	ids := identity.NewService(pool)

	own, err := ids.CreateLedger(ctx, "Payable - Employee Two", "Current Liabilities")
	if err != nil {
		t.Fatalf("create per-employee payable ledger: %v", err)
	}

	empA := createEmployee(t, svc, ctx, "E-SPLIT-1", "Aggregate")
	empB := createEmployee(t, svc, ctx, "E-SPLIT-2", "Own Ledger")
	setStructureAndElection(t, svc, ctx, empA, "2026-04-01")
	setStructureAndElectionAt(t, svc, ctx, empB, "2026-04-01", wageWithinESICeiling)

	// Opt employee B in.
	list, err := svc.ListEmployees(ctx, false)
	if err != nil {
		t.Fatalf("list employees: %v", err)
	}
	for _, e := range list {
		if e.ID != empB {
			continue
		}
		e.PayableLedgerID = own
		if err := svc.AmendEmployee(ctx, e); err != nil {
			t.Fatalf("amend employee: %v", err)
		}
	}

	res, err := svc.RunPayRun(ctx, samplePayRun(t, svc, ctx, empA, empB))
	if err != nil {
		t.Fatalf("run pay run: %v", err)
	}
	ps := postingsOf(t, pool, ctx, res.VoucherID)
	// 1,81,444 is employee A's net (2,00,000 less EPF 1,800 and TDS 16,756) and
	// 16,065 is employee B's (18,000 less EPF 1,800 and ESI 135). See the fixture
	// header for the derivation of each deduction.
	if got := ps[payrolldb.LedgerSalaryPayable][1]; !got.Equal(dec("181444")) {
		t.Errorf("aggregate Salary Payable credited %s, want 181444 (employee A only — employee B has their own ledger, "+
			"and a net that lands in the aggregate control account cannot be told apart from any other employee's)", got.StringFixed(4))
	}
	if got := ps["Payable - Employee Two"][1]; !got.Equal(dec("16065")) {
		t.Errorf("the per-employee payable ledger was credited %s, want 16065 (employee B's net pay); "+
			"a company that opted into per-employee outstandings cannot see what it owes this person", got.StringFixed(4))
	}
}

// TestPayRunRefusesASlipThatDoesNotBalance proves the per-employee identity
// check bites BEFORE anything is written, and names the employee.
//
// Without it the shortfall would surface only at COMMIT, as an aggregate, from
// posting_balanced — a constraint name with no employee in it, on a pay run of
// two hundred people.
func TestPayRunRefusesASlipThatDoesNotBalance(t *testing.T) {
	pool, ctx, _ := setup(t)
	svc := newService(pool)

	emp := createEmployee(t, svc, ctx, "E-BAL-1", "Unbalanced")
	setStructureAndElection(t, svc, ctx, emp, "2026-04-01")

	in := samplePayRun(t, svc, ctx, emp)
	// Deductions that no credit leg carries: the voucher would be short by 500.
	// Stated as a DELTA on the engine's own figures rather than as two literals,
	// so the tamper stays a 500-rupee hole whatever the fixture's salary is —
	// two stale literals would keep net = gross - deductions true and stop
	// testing anything.
	in.Slips[0].TotalDeductions = in.Slips[0].TotalDeductions.Add(dec("500"))
	in.Slips[0].NetPay = in.Slips[0].NetPay.Sub(dec("500"))

	_, err := svc.RunPayRun(ctx, in)
	if err == nil {
		t.Fatal("a pay run posted with deductions no credit leg accounts for; the voucher would be short by the difference")
	}
	if !strings.Contains(err.Error(), emp.String()) {
		t.Errorf("the refusal does not name the employee: %v", err)
	}

	// Nothing was written.
	var runs int
	if err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM pay_run`).Scan(&runs)
	}); err != nil {
		t.Fatalf("count pay runs: %v", err)
	}
	if runs != 0 {
		t.Errorf("a refused pay run left %d pay_run row(s) behind", runs)
	}
}

// TestPayrollRegisterAndPayslipReadBack proves the two read models return the
// figures that were posted, and that the ruleset versions travel with them.
//
// A payroll figure with no statement of which law produced it cannot be checked
// by anybody who was not present when it was run, which is why the versions are
// on the report and not only in the table.
func TestPayrollRegisterAndPayslipReadBack(t *testing.T) {
	pool, ctx, _ := setup(t)
	svc := newService(pool)

	emp := createEmployee(t, svc, ctx, "E-RPT-1", "Reportable")
	setStructureAndElection(t, svc, ctx, emp, "2026-04-01")
	if _, err := svc.RunPayRun(ctx, samplePayRun(t, svc, ctx, emp)); err != nil {
		t.Fatalf("run pay run: %v", err)
	}

	reg, err := svc.PayrollRegister(ctx, wageMonth)
	if err != nil {
		t.Fatalf("payroll register: %v", err)
	}
	if len(reg.Rows) != 1 {
		t.Fatalf("register has %d rows, want 1", len(reg.Rows))
	}
	// 1,81,444 = 2,00,000 gross less EPF 1,800 (12% of the 15,000 ceiling) and
	// TDS 16,756 (1,50,800 of annual tax over the nine months remaining). See
	// the fixture header.
	if !reg.Totals.Net.Equal(dec("181444")) {
		t.Errorf("register net total = %s, want 181444; the register is what a payroll operator pays the bank from, "+
			"so a wrong total here is money moved", reg.Totals.Net.StringFixed(4))
	}
	if reg.SSRuleSetVersion == "" || reg.ITRuleSetVersion == "" {
		t.Error("the register does not state which rulesets produced its figures; a payroll number with no law behind it cannot be checked later")
	}

	slip, err := svc.Payslip(ctx, wageMonth, emp)
	if err != nil {
		t.Fatalf("payslip: %v", err)
	}
	if !slip.Net.Equal(dec("181444")) {
		t.Errorf("payslip net = %s, want 181444; this is the figure the employee is told they were paid", slip.Net.StringFixed(4))
	}
	// Three lines: BASIC as the only earning, EPF_EMPLOYEE and TDS as the two
	// withheld heads. ESI_EMPLOYEE is ABSENT rather than zero — this employee is
	// above the 21,000 coverage ceiling, and "ESI 0.00" on the payslip of
	// somebody outside the scheme states the wrong fact.
	if len(slip.Lines) != 3 {
		t.Errorf("payslip has %d lines, want 3 (BASIC, EPF_EMPLOYEE, TDS — and NO ESI line, because this employee is outside the scheme rather than contributing zero): %+v",
			len(slip.Lines), slip.Lines)
	}
	// THE DEFERRALS MUST REACH THE PAYSLIP. A blank where a number should be is
	// a question the employee will ask, and the honest answer is "the rate has
	// not been notified", not "zero". Three of the 2026 Schemes' rates are
	// expressly to be notified separately — the EDLI contribution and the two
	// administrative charges — and the engine refuses to invent any of them.
	wantDeferrals := map[string]bool{
		"EDLI_CONTRIBUTION": true, "EDLI_ADMIN_CHARGES": true, "EPF_ADMIN_CHARGES": true,
	}
	if len(slip.Deferrals) != len(wantDeferrals) {
		t.Errorf("payslip carries %d deferral(s), want %d (EDLI contribution, EDLI admin charges, EPF admin charges) "+
			"recorded as deferred rather than silently zero: %+v", len(slip.Deferrals), len(wantDeferrals), slip.Deferrals)
	}
	for _, d := range slip.Deferrals {
		if !wantDeferrals[d.Component] {
			t.Errorf("payslip carries an unexpected deferral %q: %+v", d.Component, d)
		}
		if d.Citation == "" {
			t.Errorf("deferral %q carries no citation; an employee told a component could not be computed cannot check that claim", d.Component)
		}
	}
}

// ===========================================================================
// THE THREE EPFO CHARGES, IN A MONTH WHERE THEIR RATES ARE NOTIFIED
// ===========================================================================
//
// Everything above this line runs wage month JULY 2026, which resolves to
// ruleset ss-2026-07 — the window where the EDLI contribution and BOTH
// administrative charges are DEFERRED and therefore zero. Those tests are still
// doing real work (they carry the deferral assertions), but they are
// structurally incapable of noticing whether the store reads the three charge
// columns at all: the columns are zero either way.
//
// That is not hypothetical. `buildPostings` in payrun.go SELECTs
//
//	p.edli_contribution, p.edli_admin_charges, p.epf_admin_charges,
//
// and replacing those three columns with `0::numeric` — reintroducing exactly
// the bug the AggregateLegs routing was written to fix — left every payroll
// test in the repository green. The two static guards cannot see it either:
// TestExactlyOneLegBuilderDerivesTheVoucher only checks that buildPostings CALLS
// payroll.AggregateLegs, and TestEveryPersistedPayslipFigureReachesTheLegBuilder
// only checks that each PersistedPayslip FIELD NAME appears somewhere in the
// function's text. Neither can read inside a SQL string literal, and
// `&p.EDLIContribution` stays in the Scan whatever the SELECT list says.
//
// So the two tests below run wage month MAY 2026, which resolves to ss-2025-11
// — the Code-era interim window where the saved pre-2026 schemes were still in
// force and all three rates ARE notified. This is the only place in the package
// where a dropped charge changes a posted number.
//
// ---------------------------------------------------------------------------
// THE DERIVATION, FROM THE STATUTE, FOR ONE MEMBER ON A BASIC OF 18,000
// ---------------------------------------------------------------------------
// Wage month May 2026 -> ss-2025-11 (window [21 Nov 2025, 29 Jun 2026)) and
// it-ty-2026-27. The member elects PFBaseCeiling, EPS membership, ESI
// applicability and the s.202 new regime.
//
//	s.2(88): BASIC is not on the exclusion list      STATUTORY WAGE   18,000
//	EPF / EPS / EDLI wages: min(18,000, 15,000) each                  15,000
//	  EPF employee   12%    x 15,000                                   1,800
//	  EPS            8.33%  x 15,000 = 1,249.50 -> 1,250 (half away from zero)
//	  EPF employer   12%    x 15,000 = 1,800, less the EPS diversion      550
//	  EDLI premium   0.50%  x 15,000   (EDLI Scheme 1976 para 7,
//	                                    saved by the Code in this window)   75
//	  EDLI admin     0%     x 15,000   (account 22, WAIVED w.e.f.
//	                                    1 April 2017 — a rate of zero,
//	                                    not an unnotified rate)              0
//	  EPF admin      0.50%  x 15,000   (account 2, w.e.f. 1 June 2018)      75
//	ESI: latched at 18,000 <= the 21,000 coverage ceiling -> COVERED.
//	  employee 0.75% x 18,000 = 135 ; employer 3.25% x 18,000 = 585
//	s.392: 18,000 x 11 months remaining inclusive (May..Mar) = 1,98,000, less
//	  the 75,000 standard deduction = 1,23,000, inside the 4,00,000 nil slab
//	  -> TDS 0, so no TDS leg at all.
//	  deductions 1,800 + 135 = 1,935                       NET      16,065
//
// PER MEMBER, THE TWO FIGURES THIS WHOLE SECTION EXISTS FOR:
//
//	Dr Employer PF Contribution  550 + 1,250 + 75 + 0 + 75          =  1,950
//	  Cr PF Payable (statutory)  1,800 + 550 + 1,250 + 75 + 0 + 75  =  3,750
//
// If the store drops the three charges those become 1,800 and 3,600 — the
// voucher still BALANCES (both sides lose the same 150), so nothing in the
// double-entry machinery complains. What breaks is outside the books: the EPFO
// challan is remitted on the charges and the ledger is short of it by Rs 150
// per member per month, so the PF Payable balance never reconciles against what
// was actually paid to the fund.
//
// ---------------------------------------------------------------------------
// AND THE ESTABLISHMENT MINIMUM, WHICH IS NOT A PER-MEMBER FIGURE
// ---------------------------------------------------------------------------
// EPFAdminChargeMin (Rs 500 per establishment per month w.e.f. 1 June 2018
// under the saved EPF Scheme 1952) is charged on the ESTABLISHMENT. pf.go
// deliberately does not apply it — a per-member floor would multiply the charge
// by headcount — so AggregateLegs tops the aggregate up on BOTH sides. That
// makes the run's Employer PF debit and PF Payable credit a function of
// HEADCOUNT, and the two tests below take it from both directions:
//
//	2 members: charges 2 x 75 = 150  < 500 -> top-up 350, floor ENGAGED
//	7 members: charges 7 x 75 = 525 >= 500 -> top-up   0, floor INERT
//
// The seven-member case is the one that pins the per-member figures at exactly
// 1,950 / 3,750 with nothing added, and it is also what fails if the floor were
// ever moved onto the member (that would charge 7 x 500 = 3,500).
// ---------------------------------------------------------------------------

// chargesNotifiedMonth is a wage month inside ss-2025-11, where the EDLI
// premium and both administrative charges carry real rates.
var chargesNotifiedMonth = payroll.WageMonth{Year: 2026, Month: 5}

// assertChargesWereComputedNotDeferred fails if the fixture month drifted into a
// window where the three rates are unnotified.
//
// Without it, a future re-cut of the ruleset table that moved May 2026 into
// ss-2026-07 would turn every charge below into a legitimate zero, and the two
// tests would go on passing while testing nothing — which is precisely the
// failure mode that let the store-side bug survive in the first place.
func assertChargesWereComputedNotDeferred(t *testing.T, svc *payrolldb.Service, ctx context.Context, empID uuid.UUID) {
	t.Helper()
	slip, err := svc.Payslip(ctx, chargesNotifiedMonth, empID)
	if err != nil {
		t.Fatalf("read the %s payslip: %v", chargesNotifiedMonth, err)
	}
	for _, d := range slip.Deferrals {
		switch d.Component {
		case "EDLI_CONTRIBUTION", "EDLI_ADMIN_CHARGES", "EPF_ADMIN_CHARGES":
			t.Fatalf("wage month %s DEFERRED %s, so this test's charges are legitimately zero and it can no longer tell a "+
				"booked charge from a dropped one. It must run a month whose ruleset notifies all three rates (ss-2025-11): %+v",
				chargesNotifiedMonth, d.Component, d)
		}
	}
}

// TestPayRunBooksEDLIAndTheAdminChargesOntoTheVoucher is the store-side half of
// the EDLI/admin-charge fix, and the only test in the package that can fail when
// buildPostings stops reading the three charge columns.
//
// It asserts on the POSTED ROWS. RunPayRun returning nil proves nothing here:
// posting_balanced is a constraint trigger on the posting TABLE, so a voucher
// with no postings fires no balance check at all, and a voucher that is merely
// SHORT of the charges balances perfectly — both sides lose the same amount.
// The defect is invisible to every check the database performs.
//
// Two members on a BASIC of 18,000 in May 2026 (ss-2025-11). See the derivation
// above the fixture: 1,950 / 3,750 per member, plus the establishment's 350
// administrative-charge top-up to the Rs 500 monthly minimum.
func TestPayRunBooksEDLIAndTheAdminChargesOntoTheVoucher(t *testing.T) {
	pool, ctx, _ := setup(t)
	svc := newService(pool)

	empA := createEmployee(t, svc, ctx, "E-EDLI-1", "Charged One")
	empB := createEmployee(t, svc, ctx, "E-EDLI-2", "Charged Two")
	setStructureAndElectionAt(t, svc, ctx, empA, "2026-04-01", wageWithinESICeiling)
	setStructureAndElectionAt(t, svc, ctx, empB, "2026-04-01", wageWithinESICeiling)

	res, err := svc.RunPayRun(ctx, samplePayRunFor(t, svc, ctx, chargesNotifiedMonth, empA, empB))
	if err != nil {
		t.Fatalf("run the %s pay run: %v", chargesNotifiedMonth, err)
	}
	assertChargesWereComputedNotDeferred(t, svc, ctx, empA)

	ps := postingsOf(t, pool, ctx, res.VoucherID)
	if len(ps) == 0 {
		t.Fatal("the pay run posted NO posting rows at all; posting_balanced is a trigger on the posting TABLE, so a voucher " +
			"with zero postings commits silently and every figure asserted below would be missing from the books")
	}

	// The charges are 150 per member (EDLI 75 + EDLI admin 0 + EPF admin 75),
	// and the establishment's EPF administrative charge of 2 x 75 = 150 is
	// topped up by 350 to the Rs 500 monthly minimum. Both figures are stated as
	// their terms rather than as one literal so a failure says WHICH term went.
	const perMemberCharges = 150  // 75 EDLI + 0 EDLI admin + 75 EPF admin
	const adminChargeTopUp = 350  // 500 establishment minimum less 2 x 75 charged
	wantEmployerPF := dec("4250") // 2 x (550 EPF + 1,250 EPS + 150 charges) + 350
	wantPFPayable := dec("7850")  // 2 x (1,800 + 550 + 1,250 + 150 charges) + 350

	if got := ps[payrolldb.LedgerEmployerPF][0]; !got.Equal(wantEmployerPF) {
		short := wantEmployerPF.Sub(got)
		t.Errorf("Employer PF Contribution is debited %s, want %s — the pay run is short of the EPFO charges by %s. "+
			"That is the EDLI premium and the two administrative charges (Rs %d per member per month, here for 2 members) "+
			"plus the Rs %d top-up to the establishment's Rs 500 monthly administrative-charge minimum. The voucher still "+
			"BALANCES without them, because both sides lose the same amount, so nothing in the ledger complains: what breaks "+
			"is the EPFO challan, which is remitted on the charges and can no longer be reconciled against the books",
			got.StringFixed(4), wantEmployerPF.StringFixed(4), short.StringFixed(4), perMemberCharges, adminChargeTopUp)
	}
	if got := ps[payrolldb.LedgerPFPayable][1]; !got.Equal(wantPFPayable) {
		short := wantPFPayable.Sub(got)
		t.Errorf("PF Payable is credited %s, want %s — the liability to the fund is understated by %s. PF Payable IS the EPFO "+
			"remittance: employee 1,800 + employer 550 + EPS 1,250 + EDLI 75 + EDLI admin 0 + EPF admin 75 per member, plus the "+
			"Rs %d establishment top-up. An employer paying the challan in full would post a payment larger than the liability "+
			"the books carry and leave the difference stranded in a suspense position every month",
			got.StringFixed(4), wantPFPayable.StringFixed(4), short.StringFixed(4), adminChargeTopUp)
	}

	// The rest of the entry, so that a "fix" which inflated the PF legs by
	// stealing from another head cannot pass the two assertions above.
	//
	//	Dr Salaries & Wages          2 x 18,000                     = 36,000
	//	Dr Employer ESI Contribution 2 x 585                        =  1,170
	//	  Cr Salary Payable          2 x 16,065                     = 32,130
	//	  Cr ESI Payable             2 x (135 + 585)                =  1,440
	//	                             36,000 + 4,250 + 1,170 = 41,420
	//	                             32,130 + 7,850 + 1,440 = 41,420
	rest := map[string][2]string{
		payrolldb.LedgerSalariesAndWages: {"36000", "0"},
		payrolldb.LedgerEmployerESI:      {"1170", "0"},
		payrolldb.LedgerSalaryPayable:    {"0", "32130"},
		payrolldb.LedgerESIPayable:       {"0", "1440"},
	}
	for name, want := range rest {
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
	// This employee owes no tax (1,98,000 less the 75,000 standard deduction is
	// inside the nil slab), so a TDS leg here would mean money withheld from
	// somebody's wages that the statute does not ask for.
	if got, ok := ps[payrolldb.LedgerTDSOnSalaryPayable]; ok && !got[1].IsZero() {
		t.Errorf("TDS on Salary Payable is credited %s; neither employee has a s.392 liability in %s and this is wages withheld from them for nothing",
			got[1].StringFixed(4), chargesNotifiedMonth)
	}

	debits, credits := decimal.Zero, decimal.Zero
	for _, v := range ps {
		debits = debits.Add(v[0])
		credits = credits.Add(v[1])
	}
	if !debits.Equal(credits) {
		t.Fatalf("the pay run does not balance: debits %s != credits %s", debits.StringFixed(4), credits.StringFixed(4))
	}
	if want := dec("41420"); !debits.Equal(want) {
		t.Errorf("total debits = %s, want %s (36,000 gross + 4,250 employer PF incl. the EPFO charges + 1,170 employer ESI)",
			debits.StringFixed(4), want.StringFixed(4))
	}
}

// TestEPFAdminChargeMinimumIsChargedPerEstablishmentNotPerMember runs SEVEN
// members so that their administrative charges (7 x 75 = 525) already clear the
// Rs 500 monthly minimum and the floor contributes nothing.
//
// It exists for two reasons:
//
//  1. It pins the per-member figures at exactly 1,950 debit / 3,750 credit with
//     no establishment adjustment mixed in, which is the arithmetic the whole
//     EDLI/admin-charge fix is about.
//  2. The minimum is charged on the ESTABLISHMENT (EPF Scheme 1952, Rs 500 per
//     month w.e.f. 1 June 2018). Applied per MEMBER instead it would multiply by
//     headcount — 7 x 500 = 3,500 of administrative charge against a real 525 —
//     and every EPFO remittance would be overstated by the difference.
func TestEPFAdminChargeMinimumIsChargedPerEstablishmentNotPerMember(t *testing.T) {
	pool, ctx, _ := setup(t)
	svc := newService(pool)

	// Seven is the smallest headcount at which 75 per member clears 500.
	const members = 7
	ids := make([]uuid.UUID, 0, members)
	for i := 0; i < members; i++ {
		id := createEmployee(t, svc, ctx,
			fmt.Sprintf("E-FLOOR-%d", i+1), fmt.Sprintf("Floor %d", i+1))
		setStructureAndElectionAt(t, svc, ctx, id, "2026-04-01", wageWithinESICeiling)
		ids = append(ids, id)
	}

	res, err := svc.RunPayRun(ctx, samplePayRunFor(t, svc, ctx, chargesNotifiedMonth, ids...))
	if err != nil {
		t.Fatalf("run the %s pay run: %v", chargesNotifiedMonth, err)
	}
	assertChargesWereComputedNotDeferred(t, svc, ctx, ids[0])

	ps := postingsOf(t, pool, ctx, res.VoucherID)
	if len(ps) == 0 {
		t.Fatal("the pay run posted NO posting rows at all; a voucher with zero postings fires no balance check and commits silently")
	}

	// 7 x 1,950 with NOTHING added: the charges reach the minimum on their own.
	if want, got := dec("13650"), ps[payrolldb.LedgerEmployerPF][0]; !got.Equal(want) {
		t.Errorf("Employer PF Contribution is debited %s for %d members, want %s (%d x 1,950 = 550 EPF + 1,250 EPS + 75 EDLI + 0 EDLI admin + 75 EPF admin). "+
			"Too LOW means the EDLI premium or an administrative charge was dropped and the EPFO challan is short by the difference; too HIGH by a "+
			"multiple of 500 means the Rs 500 MONTHLY MINIMUM was charged per member instead of per establishment, which overstates every remittance "+
			"by 500 x (headcount - 1) = Rs %d here",
			got.StringFixed(4), members, want.StringFixed(4), members, 500*(members-1))
	}
	// 7 x 3,750, likewise untouched by the floor.
	if want, got := dec("26250"), ps[payrolldb.LedgerPFPayable][1]; !got.Equal(want) {
		t.Errorf("PF Payable is credited %s for %d members, want %s (%d x 3,750). PF Payable is the amount remitted to the EPFO, so this figure "+
			"is what the challan must equal; any difference is a reconciliation break that recurs every month",
			got.StringFixed(4), members, want.StringFixed(4), members)
	}

	debits, credits := decimal.Zero, decimal.Zero
	for _, v := range ps {
		debits = debits.Add(v[0])
		credits = credits.Add(v[1])
	}
	if !debits.Equal(credits) {
		t.Fatalf("the pay run does not balance: debits %s != credits %s", debits.StringFixed(4), credits.StringFixed(4))
	}
	// 7 x 18,000 gross + 13,650 employer PF + 7 x 585 employer ESI.
	if want := dec("143745"); !debits.Equal(want) {
		t.Errorf("total debits = %s, want %s (1,26,000 gross + 13,650 employer PF + 4,095 employer ESI)",
			debits.StringFixed(4), want.StringFixed(4))
	}
}
