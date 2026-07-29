package payrolldb

// payrun.go — persisting a pay run and posting it through the ledger.
//
// ===========================================================================
// THE TWO-TRANSACTION SEAM, AND WHY THE ORDER IS WRITE-DRAFT-THEN-POST
// ===========================================================================
// (*db.Pool).RunInTx always BEGINs a fresh pool transaction and does not nest,
// and ledger.post() opens its own. "Write the pay-run rows AND post the
// voucher" is therefore unavoidably TWO transactions. There is no arrangement
// of this code that makes it one without changing the posting path, and the
// posting path is the one seam every guarantee in the books rests on.
//
// Three orders were available:
//
//	(A) POST FIRST, RECORD AFTERWARDS. Rejected. Its crash window leaves a
//	    SALARY VOUCHER WITH NO PAY RUN BEHIND IT — money moved, with no
//	    computation record, no rulesets, no payslips. That is unauditable and
//	    un-reproducible, and nothing in the system would ever surface it: the
//	    trial balance balances, the voucher looks ordinary, and the only
//	    evidence is an absence.
//
//	(B) EXPORT ledger.PostInTx SO BOTH HALVES SHARE ONE TRANSACTION. Rejected.
//	    Changing the single posting path is the expensive option and it widens
//	    the one seam every guarantee rests on, for the benefit of one caller.
//
//	(C) WRITE DRAFT, THEN POST, THEN LINK. CHOSEN. Mint the voucher UUID
//	    app-side, write pay_run with status='DRAFT' and voucher_id ALREADY SET
//	    and posted_at NULL, write the payslips, call
//	    ledger.PostVoucherWithID(thatID, in), then UPDATE status='POSTED',
//	    posted_at=now().
//
// WHAT MAKES (C) SAFE IS THAT RECOVERY IS A PURE FUNCTION OF STATE. There are
// exactly two crash windows and each leaves a state that says what to do:
//
//	DRAFT + a voucher_head row exists for voucher_id  -> the post committed;
//	                                                     finish the linkage.
//	DRAFT + no voucher_head row                       -> the post did not
//	                                                     commit; re-post under
//	                                                     the SAME id.
//
// Neither needs a timeout, a lock, or a human. ResumePayRun implements exactly
// those two arms and nothing else, and it is safe to call any number of times —
// which is what TestPayRunIsIdempotentAcrossACrashBetweenTheTwoTransactions
// proves by posting, skipping the linkage UPDATE, re-running, and asserting
// exactly ONE voucher version exists.
//
// The replay guard itself (return the existing result when posted_at is set)
// follows internal/sync's headAny precedent.
//
// ===========================================================================
// VoucherInput.TDS MUST BE NIL, AND THAT IS NOT AN OMISSION
// ===========================================================================
// ledger.VoucherInput.TDS drives the s.194x PURCHASE/PAYMENT withholding
// engine, and ledger.post() activates it for Class in {Purchase, Payment,
// Journal}. Salary TDS is s.392: a different section, a different base (annual,
// not per-transaction) and a different rate structure. It is computed by
// internal/payroll and posted as an ORDINARY CREDIT LEG to "TDS on Salary
// Payable" like any other liability.
//
// If TDS were set here, the ledger would derive a s.194x deduction and then
// refuse the post because the posted leg did not match it — which is the good
// outcome. The bad outcome is the one this comment exists to prevent: somebody
// "fixing" that refusal by making the legs agree with the s.194x engine, and
// deducting the wrong tax from every employee.
//
// GST, Inventory, PhysicalCount and BillAllocations are nil for the ordinary
// reasons: a pay run is not a supply, moves no goods, counts no shelf, and
// creates no bill-wise outstanding (the net-pay liability is a control balance,
// not a bill somebody references).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/payroll"
	"github.com/khata/cored/internal/platform/db"
)

// Pay run statuses, mirroring the pay_run.status CHECK in migration 0034.
const (
	// StatusDraft: the rows exist, the voucher may or may not. See the header.
	StatusDraft = "DRAFT"
	// StatusPosted: the voucher is committed and linked.
	StatusPosted = "POSTED"
	// StatusReversed: the voucher was deleted (tombstoned) and the month is
	// free to be re-run. The payslips STAY — they are immutable, and the
	// reversal is meant to leave both the wrong figure and the right one
	// visible.
	StatusReversed = "REVERSED"
)

// ErrPayRunNotFound is returned when no pay_run row exists for the id in the
// company in context. RLS makes another tenant's run invisible rather than
// forbidden, so this is also the answer to a cross-tenant id — which is the
// correct answer to give either way.
var ErrPayRunNotFound = errors.New("payrolldb: pay run not found")

// ErrPayRunUnbalanced is returned when a slip's own figures do not satisfy
// net = gross - deductions, or the deductions do not account for themselves.
//
// It is checked HERE, before anything is written, rather than left to
// posting_balanced at commit. That trigger would catch the aggregate — but it
// fires at COMMIT, names a constraint, and cannot say WHICH EMPLOYEE'S numbers
// are wrong. A payroll error is always about one person.
//
// IT IS NOT SUFFICIENT ON ITS OWN, and that is the whole reason
// ErrSlipNotFromEngine exists below: both identities it checks hold for ANY
// internally consistent set of invented numbers.
var ErrPayRunUnbalanced = errors.New("payrolldb: payslip does not balance")

// ErrSlipNotFromEngine is returned when a supplied SlipInput differs from the
// payslip internal/payroll computes for the same employee, month and facts.
//
// ===========================================================================
// WHY A HAND-SUPPLIED PAYSLIP IS REFUSED RATHER THAN TRUSTED
// ===========================================================================
// SlipInput has twenty-odd money fields, and until this check existed the only
// thing standing between a caller and the immutable payslip table was
// SlipInput.validate — two SELF-CONSISTENCY identities:
//
//	net = gross - deductions
//	deductions = TDS + employee PF + employee ESI
//
// BOTH OF THEM HOLD FOR ANY INTERNALLY CONSISTENT SET OF INVENTED NUMBERS. A
// caller could halve every employee's PF, double the TDS, or zero the ESI, keep
// the two identities true, and the run would validate, write to tables that
// cannot be updated or deleted, post a balanced voucher, and be filed from. The
// statutory engine — the part that was tested against a corpus by two
// independent derivations — would not have run at all.
//
// So the write path RE-DERIVES every slip from the database and the wage month
// and refuses on ANY difference, naming the field and both figures. There is no
// tolerance: every figure was rounded once, by the statute's own rule, inside
// the leaf that produced it, and a paisa of slack is a paisa per employee per
// month that nobody chose.
//
// The attendance and declaration facts the database does not hold travel on
// PayRunInput.MonthInputs so the re-derivation can see exactly what the
// computation saw. Those are FACTS, not computed money — see compute.go.
var ErrSlipNotFromEngine = errors.New("payrolldb: payslip was not produced by the payroll engine")

// ComponentLine is one line behind a payslip's totals.
type ComponentLine struct {
	Code string
	// Type names the line. For an earning it is a payroll.ComponentType; for a
	// computed statutory line (EPF_EMPLOYEE, ESI_EMPLOYEE, TDS) it is that
	// line's own name, which is why the column carries no CHECK — constraining
	// it to the earnings vocabulary would forbid exactly the lines a payslip
	// exists to show.
	Type      string
	Tag       payroll.ComponentTag
	Direction string // EARNING | DEDUCTION
	Amount    decimal.Decimal
}

// Direction values for a ComponentLine.
const (
	DirectionEarning   = "EARNING"
	DirectionDeduction = "DEDUCTION"
)

// SlipInput is one employee's computed result, as this package persists it.
//
// IT IS THE ENGINE'S OUTPUT, MAPPED — never a second computation. The
// arithmetic behind every field is internal/payroll's, checked against
// corpora/payroll by two independent derivations. This package's only
// arithmetic is SUMMATION: it adds these figures across employees to form the
// voucher's legs, and it checks the identities below. Anything more would be a
// second implementation of the payroll engine living behind a database.
type SlipInput struct {
	EmployeeID uuid.UUID

	// DaysPaid / LOPDays feed the ESI return's "No. of Days for which wages
	// paid" and the ECR's NCP days. They are results of the run, not
	// annotations on it.
	DaysPaid decimal.Decimal
	LOPDays  decimal.Decimal

	GrossEarnings   decimal.Decimal
	TotalDeductions decimal.Decimal
	NetPay          decimal.Decimal

	// The Code on Social Security wage and the four bases derived from it. EPF,
	// EPS and EDLI wages are THREE DIFFERENT NUMBERS and are never collapsed.
	StatutoryWages decimal.Decimal
	EPFWages       decimal.Decimal
	EPSWages       decimal.Decimal
	EDLIWages      decimal.Decimal
	ESIWages       decimal.Decimal

	// Statutory and voluntary provident fund kept apart on BOTH sides, so Form V
	// and the remittance can distinguish them. A netted figure cannot be
	// un-netted later.
	EPFEmployeeStatutory decimal.Decimal
	EPFEmployeeVoluntary decimal.Decimal
	EPFEmployerStatutory decimal.Decimal
	EPFEmployerVoluntary decimal.Decimal
	EPSEmployer          decimal.Decimal

	// THE EMPLOYER'S THREE CHARGES TO THE FUND: the EDLI premium and the two
	// administrative charges. They are NOT contributions into the member's
	// account, no PFResult total carries them (pf.go holds them only as tagged
	// components, so an unnotified rate can never look like a zero one), and
	// they are NEVER deducted from the employee.
	//
	// They are persisted — migration 0036 — because the voucher is built from
	// the STORED payslips in a later transaction, so a charge with no column is
	// a charge the pay run cannot book. Before those columns existed the product
	// posted the employer's EPF and EPS and nothing at all for EDLI or
	// administration, which understated the EPFO liability by around Rs 150 per
	// member per month wherever the rates are notified.
	EDLIContribution decimal.Decimal
	EDLIAdminCharges decimal.Decimal
	EPFAdminCharges  decimal.Decimal

	ESIEmployee decimal.Decimal
	ESIEmployer decimal.Decimal

	// TDS is the s.392 deduction for this month. See the header for why it is
	// never ledger.VoucherInput.TDS.
	TDS decimal.Decimal

	// Deferrals is what the engine REFUSED TO GUESS — today, the EDLI
	// contribution, whose rate the EDLI Scheme 2026 says will be notified
	// separately. Recorded rather than dropped: a payslip silently showing zero
	// for an unnotified component is indistinguishable from one where the
	// component genuinely does not apply.
	Deferrals []payroll.Deferral

	Components []ComponentLine
}

// employeeDeductions is the deduction side of a slip as the ledger sees it:
// what was withheld from gross and therefore does not reach the employee.
func (s SlipInput) employeeDeductions() decimal.Decimal {
	return sumOf(s.TDS, s.EPFEmployeeStatutory, s.EPFEmployeeVoluntary, s.ESIEmployee)
}

// validate checks the two identities that make the voucher balance, per
// employee, with the employee named.
//
// (1) net = gross - deductions. The voucher's debit side is gross and its
//
//	credit side is net + the withheld heads; if this fails the entry cannot
//	balance and no arrangement of the legs will fix it.
//
// (2) deductions = TDS + employee PF (statutory + voluntary) + employee ESI.
//
//	This is what says the withheld heads ACCOUNT FOR the difference. Without
//	it, a slip could satisfy (1) with a deduction total that no credit leg
//	carries, and the voucher would be short by exactly that amount — caught
//	only at commit, as an aggregate, with no employee named.
func (s SlipInput) validate(i int) error {
	if s.EmployeeID == uuid.Nil {
		return fmt.Errorf("payrolldb: payslip %d: employee id is required", i+1)
	}
	for name, v := range map[string]decimal.Decimal{
		"gross_earnings": s.GrossEarnings, "total_deductions": s.TotalDeductions,
		"net_pay": s.NetPay, "tds": s.TDS,
		"epf_employee_statutory": s.EPFEmployeeStatutory,
		"epf_employee_voluntary": s.EPFEmployeeVoluntary,
		"epf_employer_statutory": s.EPFEmployerStatutory,
		"epf_employer_voluntary": s.EPFEmployerVoluntary,
		"eps_employer":           s.EPSEmployer,
		"edli_contribution":      s.EDLIContribution,
		"edli_admin_charges":     s.EDLIAdminCharges,
		"epf_admin_charges":      s.EPFAdminCharges,
		"esi_employee":           s.ESIEmployee, "esi_employer": s.ESIEmployer,
	} {
		if v.IsNegative() {
			return fmt.Errorf("payrolldb: payslip %d (employee %s): %s is negative (%s); a negative statutory amount is a sign error, and posting it would put the leg on the wrong side of the entry",
				i+1, s.EmployeeID, name, v.StringFixed(money))
		}
	}
	if want := s.GrossEarnings.Sub(s.TotalDeductions); !want.Equal(s.NetPay) {
		return fmt.Errorf("%w: employee %s: net_pay %s != gross_earnings %s - total_deductions %s (= %s)",
			ErrPayRunUnbalanced, s.EmployeeID,
			s.NetPay.StringFixed(money), s.GrossEarnings.StringFixed(money),
			s.TotalDeductions.StringFixed(money), want.StringFixed(money))
	}
	if want := s.employeeDeductions(); !want.Equal(s.TotalDeductions) {
		return fmt.Errorf("%w: employee %s: total_deductions %s is not accounted for by the withheld heads (TDS %s + employee PF %s + employee ESI %s = %s); the voucher would be short by the difference",
			ErrPayRunUnbalanced, s.EmployeeID,
			s.TotalDeductions.StringFixed(money), s.TDS.StringFixed(money),
			s.EPFEmployeeStatutory.Add(s.EPFEmployeeVoluntary).StringFixed(money),
			s.ESIEmployee.StringFixed(money), want.StringFixed(money))
	}
	return nil
}

// PayRunInput is one wage month's pay run as this package is asked to persist
// and post it.
type PayRunInput struct {
	Month     payroll.WageMonth
	CreatedBy string
	Narration string
	Slips     []SlipInput

	// MonthInputs carries the facts the database does not hold — attendance,
	// the ex-s.192(2) particulars of a former employer, the employee's own
	// declarations — one row per slip, so the write path can RE-DERIVE each
	// payslip and refuse anything the engine did not produce. See
	// ErrSlipNotFromEngine.
	//
	// It may be EMPTY, and then the re-derivation runs with each employee's
	// attendance taken from their slip and every declaration at zero. That is
	// the right default rather than a bypass: a run that genuinely used a
	// declaration and did not carry it here will be REFUSED, because the
	// verification will legitimately compute a different tax. Populate it from
	// ComputeSlips (ComputeAndRunPayRun does) and the two always agree.
	MonthInputs []MonthInput
}

// PayRunResult identifies the persisted run and the voucher it posted as.
type PayRunResult struct {
	PayRunID  uuid.UUID
	VoucherID uuid.UUID
	VoucherNo string
	VersionNo int
	Status    string
	// AlreadyPosted is true when the replay guard short-circuited: the run was
	// already POSTED and nothing was posted again.
	AlreadyPosted bool
}

// RunPayRun persists a pay run as DRAFT and then posts it. See the file header
// for the two-transaction seam.
func (s *Service) RunPayRun(ctx context.Context, in PayRunInput) (PayRunResult, error) {
	payRunID, err := s.CreateDraftPayRun(ctx, in)
	if err != nil {
		return PayRunResult{}, err
	}
	return s.ResumePayRun(ctx, payRunID, in.CreatedBy, in.Narration)
}

// CreateDraftPayRun performs TRANSACTION ONE only: it validates the slips,
// resolves the rulesets, mints both ids and writes the pay_run row (DRAFT, with
// voucher_id already set) and its payslips. It posts nothing.
//
// It is separate from RunPayRun for two reasons, and the second is the one that
// matters. The first is ordinary product shape: a payroll operator computes a
// month, reviews the register, and posts. The second is that the crash window
// this whole design is built around is EXACTLY the gap between this function
// returning and ResumePayRun committing — so it has to be reachable on its own,
// or the recovery path can only ever be exercised by a test that fakes state
// rather than by one that reproduces it.
func (s *Service) CreateDraftPayRun(ctx context.Context, in PayRunInput) (uuid.UUID, error) {
	if len(in.Slips) == 0 {
		return uuid.Nil, errors.New("payrolldb: a pay run with no payslips computes nothing and would post an empty voucher")
	}
	for i, sl := range in.Slips {
		if err := sl.validate(i); err != nil {
			return uuid.Nil, err
		}
	}
	// THE ENGINE GATE. It runs BEFORE anything is written and before the
	// rulesets are stamped, so a run whose numbers did not come from
	// internal/payroll leaves no pay_run row, no payslip and no voucher behind.
	// See ErrSlipNotFromEngine for why validate() above cannot be the only
	// check.
	if err := s.verifySlipsAgainstEngine(ctx, in); err != nil {
		return uuid.Nil, err
	}
	rulesets, err := ResolveRuleSets(in.Month)
	if err != nil {
		return uuid.Nil, err
	}
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return uuid.Nil, db.ErrNoCompany
	}

	payRunID, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("payrolldb: new pay run id: %w", err)
	}
	// THE VOUCHER ID IS MINTED HERE, APP-SIDE, and written with the DRAFT. That
	// is what makes the crash window recoverable: the draft NAMES the voucher it
	// is going to be, so a resume can look for it instead of guessing.
	voucherID, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("payrolldb: new voucher id: %w", err)
	}

	// Transaction 1: the draft and its payslips.
	err = s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`INSERT INTO pay_run
			   (id, company_id, wage_month, status, voucher_id,
			    ss_ruleset_version, it_ruleset_version, created_by)
			 VALUES ($1, $2, $3::date, $4, $5, $6, $7, $8)`,
			payRunID, companyID, in.Month.First().Format("2006-01-02"),
			StatusDraft, voucherID,
			rulesets.SSVersion(), rulesets.ITVersion(), in.CreatedBy); err != nil {
			return fmt.Errorf("payrolldb: insert pay run: %w", err)
		}
		for i, sl := range in.Slips {
			if err := insertPayslip(ctx, tx, companyID, payRunID, sl); err != nil {
				return fmt.Errorf("payrolldb: payslip %d: %w", i+1, err)
			}
		}
		return nil
	})
	if err != nil {
		return uuid.Nil, err
	}
	return payRunID, nil
}

// ---------------------------------------------------------------------------
// The engine gate.
// ---------------------------------------------------------------------------

// verifySlipsAgainstEngine re-derives every slip in the run from the database
// and refuses the run on any difference. See ErrSlipNotFromEngine.
//
// It reads the SAME state the computation read and calls the SAME function
// (ComputeSlips), which is exactly the point: the property being established is
// not "these numbers are statutorily correct" — internal/payroll's corpus
// proves that — but "these numbers ARE internal/payroll's, for this company,
// this month and these employees". A caller who computed a run, edited one
// field and posted it is refused; a caller who never called the engine at all
// is refused; a caller who computed against a different company's structures is
// refused, because RLS confines the re-derivation to the company in context.
//
// It is deliberately NOT skippable by any flag, environment variable or
// unexported hook. A gate with a bypass is a gate that will be bypassed on the
// afternoon somebody's payroll has to go out.
func (s *Service) verifySlipsAgainstEngine(ctx context.Context, in PayRunInput) error {
	inputs := in.MonthInputs
	if len(inputs) == 0 {
		// No facts supplied: re-derive with each employee's attendance read off
		// their own slip and every declaration at zero. Attendance is on
		// SlipInput because the ECR and the ESI return both carry it, so it is
		// available here without the caller repeating it.
		inputs = make([]MonthInput, 0, len(in.Slips))
		for _, sl := range in.Slips {
			inputs = append(inputs, MonthInput{
				EmployeeID: sl.EmployeeID,
				PaidDays:   sl.DaysPaid,
				LOPDays:    sl.LOPDays,
			})
		}
	}

	byEmployee := make(map[uuid.UUID]MonthInput, len(inputs))
	for _, mi := range inputs {
		byEmployee[mi.EmployeeID] = mi
	}
	if len(byEmployee) != len(in.Slips) {
		return fmt.Errorf("%w: the run has %d payslips but %d distinct month inputs; "+
			"the write path re-derives every payslip and needs exactly one set of facts per employee",
			ErrSlipNotFromEngine, len(in.Slips), len(byEmployee))
	}
	for _, sl := range in.Slips {
		if _, ok := byEmployee[sl.EmployeeID]; !ok {
			return fmt.Errorf("%w: payslip for employee %s carries no month input, so it cannot be re-derived",
				ErrSlipNotFromEngine, sl.EmployeeID)
		}
	}

	computed, _, err := s.ComputeSlips(ctx, in.Month, inputs)
	if err != nil {
		return fmt.Errorf("re-derive the %s pay run to verify it against the payroll engine: %w", in.Month, err)
	}
	want := make(map[uuid.UUID]SlipInput, len(computed))
	for _, c := range computed {
		want[c.EmployeeID] = c
	}
	for _, got := range in.Slips {
		w, ok := want[got.EmployeeID]
		if !ok {
			// Unreachable while ComputeSlips returns one slip per input, and
			// asserted rather than assumed: a silent miss here would let an
			// employee's figures through unverified.
			return fmt.Errorf("%w: the engine produced no payslip for employee %s in %s",
				ErrSlipNotFromEngine, got.EmployeeID, in.Month)
		}
		if err := diffSlip(got, w); err != nil {
			return fmt.Errorf("%w: employee %s, wage month %s: %s. "+
				"A pay run's figures must be the engine's; this one was supplied or edited by the caller and will not be posted",
				ErrSlipNotFromEngine, got.EmployeeID, in.Month, err)
		}
	}
	return nil
}

// diffSlip reports the FIRST field on which a supplied slip differs from the
// engine's, with both figures.
//
// The order of the fields is the order a payroll error is diagnosed in — the
// wage bases first, because everything downstream is a percentage of them, then
// the contributions, then the totals. Naming the field and both numbers is what
// makes the refusal actionable; "the pay run does not match" on a run of two
// hundred people is not.
func diffSlip(got, want SlipInput) error {
	for _, f := range []struct {
		name      string
		got, want decimal.Decimal
	}{
		{"statutory_wages", got.StatutoryWages, want.StatutoryWages},
		{"epf_wages", got.EPFWages, want.EPFWages},
		{"eps_wages", got.EPSWages, want.EPSWages},
		{"edli_wages", got.EDLIWages, want.EDLIWages},
		{"esi_wages", got.ESIWages, want.ESIWages},
		{"epf_employee_statutory", got.EPFEmployeeStatutory, want.EPFEmployeeStatutory},
		{"epf_employee_voluntary", got.EPFEmployeeVoluntary, want.EPFEmployeeVoluntary},
		{"epf_employer_statutory", got.EPFEmployerStatutory, want.EPFEmployerStatutory},
		{"epf_employer_voluntary", got.EPFEmployerVoluntary, want.EPFEmployerVoluntary},
		{"eps_employer", got.EPSEmployer, want.EPSEmployer},
		// The employer's charges to the fund. They are checked here for the same
		// reason as everything else: they are posted, they are remitted, and a
		// caller who supplied them by hand would otherwise be trusted with the
		// one part of the entry that no PFResult total cross-checks.
		{"edli_contribution", got.EDLIContribution, want.EDLIContribution},
		{"edli_admin_charges", got.EDLIAdminCharges, want.EDLIAdminCharges},
		{"epf_admin_charges", got.EPFAdminCharges, want.EPFAdminCharges},
		{"esi_employee", got.ESIEmployee, want.ESIEmployee},
		{"esi_employer", got.ESIEmployer, want.ESIEmployer},
		{"tds", got.TDS, want.TDS},
		{"gross_earnings", got.GrossEarnings, want.GrossEarnings},
		{"total_deductions", got.TotalDeductions, want.TotalDeductions},
		{"net_pay", got.NetPay, want.NetPay},
		{"days_paid", got.DaysPaid, want.DaysPaid},
		{"lop_days", got.LOPDays, want.LOPDays},
	} {
		if !f.got.Equal(f.want) {
			return fmt.Errorf("%s is %s but the engine computes %s",
				f.name, f.got.StringFixed(money), f.want.StringFixed(money))
		}
	}

	// The component lines are checked too, not only the totals. They are what
	// the payslip SHOWS the employee and what the ECR and Form V itemise, so a
	// run whose totals agree while its lines do not is a payslip that does not
	// explain its own net pay.
	if len(got.Components) != len(want.Components) {
		return fmt.Errorf("the payslip has %d component lines but the engine produces %d",
			len(got.Components), len(want.Components))
	}
	for i := range got.Components {
		g, w := got.Components[i], want.Components[i]
		if g.Code != w.Code || g.Type != w.Type || g.Tag != w.Tag || g.Direction != w.Direction {
			return fmt.Errorf("component line %d is %s/%s/%s/%s but the engine produces %s/%s/%s/%s",
				i+1, g.Code, g.Type, g.Tag, g.Direction, w.Code, w.Type, w.Tag, w.Direction)
		}
		if !g.Amount.Equal(w.Amount) {
			return fmt.Errorf("component line %d (%s) is %s but the engine computes %s",
				i+1, g.Code, g.Amount.StringFixed(money), w.Amount.StringFixed(money))
		}
	}

	// And the deferrals. A deferral is the engine REFUSING to invent a number;
	// dropping one turns "the EDLI rate has not been notified" into a silent
	// zero on a payslip and in a statutory return, which is precisely the
	// substitution Deferral exists to prevent.
	if len(got.Deferrals) != len(want.Deferrals) {
		return fmt.Errorf("the payslip records %d deferral(s) but the engine produces %d; "+
			"a dropped deferral turns \"this component could not be computed\" into a silent zero",
			len(got.Deferrals), len(want.Deferrals))
	}
	for i := range got.Deferrals {
		g, w := got.Deferrals[i], want.Deferrals[i]
		if g.Component != w.Component || g.Reason != w.Reason || g.Citation != w.Citation {
			return fmt.Errorf("deferral %d is %q but the engine produces %q", i+1, g.Component, w.Component)
		}
	}
	return nil
}

// ResumePayRun drives a pay run from whatever state it is in to POSTED. It is
// the entry point for both the ordinary path (called by RunPayRun immediately
// after the draft commits) and CRASH RECOVERY, and it is safe to call any
// number of times.
//
// The three arms, and they are the whole of the logic:
//
//	POSTED                        -> return the existing result. THE REPLAY
//	                                 GUARD. Posting again would append a second
//	                                 voucher version for the same month's wages.
//	DRAFT + voucher_head exists   -> the post committed before the crash; set
//	                                 posted_at. NOTHING is posted again.
//	DRAFT + no voucher_head       -> post under the SAME voucher id, then link.
//
// REVERSED is refused rather than resumed: a reversed run's voucher has been
// tombstoned deliberately, and re-posting it would resurrect the month's wages
// under an id somebody had already decided to withdraw.
func (s *Service) ResumePayRun(ctx context.Context, payRunID uuid.UUID, createdBy, narration string) (PayRunResult, error) {
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return PayRunResult{}, db.ErrNoCompany
	}

	var (
		status      string
		voucherID   uuid.UUID
		wageMonth   time.Time
		posted      bool
		postings    []ledger.PostingInput
		headExists  bool
		headVersion int
	)
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var vid *uuid.UUID
		var postedAt *time.Time
		switch err := tx.QueryRow(ctx,
			`SELECT status, voucher_id, wage_month, posted_at
			   FROM pay_run WHERE id = $1`, payRunID).
			Scan(&status, &vid, &wageMonth, &postedAt); {
		case errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("%w: %s", ErrPayRunNotFound, payRunID)
		case err != nil:
			return fmt.Errorf("payrolldb: read pay run: %w", err)
		}
		posted = postedAt != nil
		if vid != nil {
			voucherID = *vid
		}
		if status == StatusPosted {
			return nil // replay guard; postings are not needed
		}
		if status == StatusReversed {
			return fmt.Errorf("payrolldb: pay run %s is REVERSED; its voucher was deliberately withdrawn and re-posting it would resurrect the month's wages", payRunID)
		}
		if voucherID == uuid.Nil {
			return fmt.Errorf("payrolldb: pay run %s is DRAFT with no voucher id; the draft must name the voucher it is going to be or a resume cannot find it", payRunID)
		}
		// Does the voucher already exist? This is the whole of crash recovery.
		if err := tx.QueryRow(ctx,
			`SELECT true, COALESCE((SELECT MAX(version_no) FROM voucher_version
			                         WHERE company_id = $1 AND voucher_id = $2), 0)
			   FROM voucher_head WHERE company_id = $1 AND voucher_id = $2`,
			companyID, voucherID).Scan(&headExists, &headVersion); err != nil &&
			!errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("payrolldb: read voucher head: %w", err)
		}
		if headExists {
			return nil // nothing to post; just link
		}
		ps, err := buildPostings(ctx, tx, companyID, payRunID)
		if err != nil {
			return err
		}
		postings = ps
		return nil
	})
	if err != nil {
		return PayRunResult{}, err
	}

	// Arm 1: already posted. Return what is there.
	if status == StatusPosted && posted {
		return s.readResult(ctx, payRunID, true)
	}

	// Arm 3: post under the SAME id.
	if !headExists {
		in := ledger.VoucherInput{
			Class:       ledger.Payroll,
			VoucherDate: LastDayOf(wageMonth).Format("2006-01-02"),
			Narration:   narration,
			CreatedBy:   createdBy,
			Postings:    postings,
			// TDS/GST/Inventory/PhysicalCount/BillAllocations all nil. See the
			// file header — VoucherInput.TDS is the s.194x engine and would
			// refuse this voucher, or worse, be "fixed" into deducting the wrong
			// tax from every employee.
		}
		if _, err := s.ledger.PostVoucherWithID(ctx, voucherID, in); err != nil {
			return PayRunResult{}, fmt.Errorf("payrolldb: post pay run %s: %w", payRunID, err)
		}
	}

	// Arms 2 and 3 converge: link the run to the voucher that now exists.
	err = s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE pay_run SET status = $2, posted_at = now()
			  WHERE id = $1 AND status = $3`,
			payRunID, StatusPosted, StatusDraft)
		return err
	})
	if err != nil {
		return PayRunResult{}, fmt.Errorf("payrolldb: link pay run %s to its voucher: %w", payRunID, err)
	}
	return s.readResult(ctx, payRunID, false)
}

// AlterPayRunVoucher re-states the pay run's voucher through ledger.AlterVoucher
// with the SAME legs and a new narration.
//
// A pay run's voucher is alterable exactly like any other voucher — the version
// chain, the hash chain and the freeze gate all apply, because nothing here
// goes around ledger.post(). What is NOT alterable is the COMPUTATION: payslip
// and payslip_component are immutable, so changing what somebody was paid means
// reversing the run and re-running the month, which leaves both the wrong
// figure and the right one visible. That asymmetry is deliberate: a narration
// is a label, a payslip is what an employee was told they were paid and what a
// statutory return was filed from.
func (s *Service) AlterPayRunVoucher(ctx context.Context, payRunID uuid.UUID, narration, createdBy string) (PayRunResult, error) {
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return PayRunResult{}, db.ErrNoCompany
	}
	var voucherID uuid.UUID
	var wageMonth time.Time
	var postings []ledger.PostingInput
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var vid *uuid.UUID
		var status string
		switch err := tx.QueryRow(ctx,
			`SELECT status, voucher_id, wage_month FROM pay_run WHERE id = $1`, payRunID).
			Scan(&status, &vid, &wageMonth); {
		case errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("%w: %s", ErrPayRunNotFound, payRunID)
		case err != nil:
			return fmt.Errorf("payrolldb: read pay run: %w", err)
		}
		if status != StatusPosted || vid == nil {
			return fmt.Errorf("payrolldb: pay run %s is %s; only a POSTED run has a voucher to alter", payRunID, status)
		}
		voucherID = *vid
		ps, err := buildPostings(ctx, tx, companyID, payRunID)
		if err != nil {
			return err
		}
		postings = ps
		return nil
	})
	if err != nil {
		return PayRunResult{}, err
	}
	if _, err := s.ledger.AlterVoucher(ctx, voucherID, ledger.VoucherInput{
		Class:       ledger.Payroll,
		VoucherDate: LastDayOf(wageMonth).Format("2006-01-02"),
		Narration:   narration,
		CreatedBy:   createdBy,
		Postings:    postings,
	}); err != nil {
		return PayRunResult{}, fmt.Errorf("payrolldb: alter pay run voucher %s: %w", voucherID, err)
	}
	return s.readResult(ctx, payRunID, false)
}

// ReversePayRun tombstones the pay run's voucher and marks the run REVERSED,
// which frees the month for a corrected run (the one-live-run-per-month unique
// index is partial on status <> 'REVERSED' for exactly this reason).
//
// The PAYSLIPS ARE NOT DELETED and cannot be — they are immutable. That is the
// point: a reversal leaves the wrong figure and the right one both visible,
// which is what an auditor and an employee each need. Deleting them would make
// a correction indistinguishable from a month that never ran.
func (s *Service) ReversePayRun(ctx context.Context, payRunID uuid.UUID, createdBy string) error {
	var voucherID uuid.UUID
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var vid *uuid.UUID
		var status string
		switch err := tx.QueryRow(ctx,
			`SELECT status, voucher_id FROM pay_run WHERE id = $1`, payRunID).Scan(&status, &vid); {
		case errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("%w: %s", ErrPayRunNotFound, payRunID)
		case err != nil:
			return fmt.Errorf("payrolldb: read pay run: %w", err)
		}
		if status == StatusReversed {
			return nil // idempotent
		}
		if vid != nil {
			voucherID = *vid
		}
		return nil
	})
	if err != nil {
		return err
	}
	if voucherID != uuid.Nil {
		if _, derr := s.ledger.DeleteVoucher(ctx, voucherID, createdBy); derr != nil &&
			!errors.Is(derr, ledger.ErrVoucherNotFound) {
			return fmt.Errorf("payrolldb: reverse pay run %s: %w", payRunID, derr)
		}
	}
	err = s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, uerr := tx.Exec(ctx,
			`UPDATE pay_run SET status = $2 WHERE id = $1`, payRunID, StatusReversed)
		return uerr
	})
	if err != nil {
		return fmt.Errorf("payrolldb: mark pay run %s reversed: %w", payRunID, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// The legs.
// ---------------------------------------------------------------------------

// buildPostings derives the pay run's debit and credit legs FROM THE PERSISTED
// PAYSLIPS, inside the caller's transaction.
//
// ===========================================================================
// THERE IS EXACTLY ONE LEG BUILDER, AND IT IS payroll.AggregateLegs
// ===========================================================================
// This function reads rows, resolves ledger IDS and maps the result onto
// ledger.PostingInput. IT DOES NO ARITHMETIC AT ALL: which amount lands on
// which side is decided by payroll.AggregateLegs and by nothing here.
//
// It used to derive its own legs by summing the payslip columns, and that
// second derivation is the whole reason this comment exists. The pure package
// debits the EDLI premium and the EPF/EDLI administrative charges — they are
// employer liabilities to the fund, carried ONLY as tagged components because
// no PFResult total holds them — and this function had no column for any of the
// three and silently dropped them. The books were therefore short the whole
// EPFO charge on EVERY pay run the product posted (around Rs 150 per member per
// month wherever those rates are notified), while the pure package's tests
// proved a fix that had no caller.
//
// So: a figure the store cannot supply is a figure the voucher cannot carry,
// migration 0036 gave the three charges their columns, and the legs come from
// the one builder. TestExactlyOneLegBuilderDerivesTheVoucher (in
// internal/payroll) fails the build if a second derivation reappears here.
//
// It is SINGLE-SOURCED and used by both the post and the alter path, so a
// restated voucher can never carry different legs from the one it supersedes.
//
// THE ENTRY, and why each side is where it is:
//
//	Dr Salaries & Wages           gross earnings, all employees
//	Dr Employer PF Contribution   employer EPF (statutory + voluntary) + EPS
//	                              + the EDLI premium + the EPF/EDLI admin charges
//	Dr Employer ESI Contribution  the employer's 3.25% share
//	  Cr Salary Payable           net pay (per employee where they have their
//	                              own payable ledger, aggregate otherwise)
//	  Cr TDS on Salary Payable    s.392
//	  Cr PF Payable               employee + employer PF and the employer's
//	                              charges, statutory and voluntary as SEPARATE
//	                              lines
//	  Cr ESI Payable              employee + employer ESI
//
// The employer's contributions are a COST and a LIABILITY simultaneously. The
// employee's share is NOT a cost — it is part of gross pay that is withheld —
// which is why it reaches PF Payable through the net-pay arithmetic and not
// through a second expense debit. Booking the employee share as an expense
// would overstate the wage bill by 12% of the PF wage in every company.
func buildPostings(ctx context.Context, tx pgx.Tx, companyID, payRunID uuid.UUID) ([]ledger.PostingInput, error) {
	led, err := controlLedgerIDs(ctx, tx)
	if err != nil {
		return nil, err
	}

	// The run's stamped rulesets and wage month. They travel onto every
	// reconstructed payslip so that AggregateLegs' mixed-ruleset refusal means
	// something on a re-post, and so an error can name the month.
	var ssVersion, itVersion string
	var wageMonth time.Time
	if err := tx.QueryRow(ctx,
		`SELECT ss_ruleset_version, it_ruleset_version, wage_month
		   FROM pay_run WHERE company_id = $1 AND id = $2`,
		companyID, payRunID).Scan(&ssVersion, &itVersion, &wageMonth); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: %s", ErrPayRunNotFound, payRunID)
		}
		return nil, fmt.Errorf("payrolldb: read pay run for posting: %w", err)
	}
	month := payroll.WageMonth{Year: wageMonth.Year(), Month: wageMonth.Month()}

	rows, err := tx.Query(ctx,
		`SELECT p.employee_id, p.gross_earnings, p.net_pay, p.tds,
		        p.epf_employee_statutory, p.epf_employee_voluntary,
		        p.epf_employer_statutory, p.epf_employer_voluntary,
		        p.eps_employer,
		        p.edli_contribution, p.edli_admin_charges, p.epf_admin_charges,
		        p.esi_employee, p.esi_employer,
		        e.payable_ledger_id
		   FROM payslip p
		   JOIN employee e ON e.company_id = p.company_id AND e.id = p.employee_id
		  WHERE p.company_id = $1 AND p.pay_run_id = $2
		  ORDER BY e.code`,
		companyID, payRunID)
	if err != nil {
		return nil, fmt.Errorf("payrolldb: read payslips for posting: %w", err)
	}
	defer rows.Close()

	// ledgerIDs maps every ledger NAME the leg builder can emit onto an id: the
	// seven control ledgers by name, plus each employee payable ledger by the
	// string form of its own id. Resolution stays here because it needs a
	// company and a transaction, which the pure package has neither of.
	ledgerIDs := make(map[string]uuid.UUID, len(led)+4)
	for name, id := range led {
		ledgerIDs[name] = id
	}

	var slips []payroll.Payslip
	for rows.Next() {
		var employeeID uuid.UUID
		var p payroll.PersistedPayslip
		var payable *uuid.UUID
		if err := rows.Scan(&employeeID,
			&p.GrossEarnings, &p.NetPay, &p.TDS,
			&p.EPFEmployeeStatutory, &p.EPFEmployeeVoluntary,
			&p.EPFEmployerStatutory, &p.EPFEmployerVoluntary, &p.EPSEmployer,
			&p.EDLIContribution, &p.EDLIAdminCharges, &p.EPFAdminCharges,
			&p.ESIEmployee, &p.ESIEmployer, &payable); err != nil {
			return nil, fmt.Errorf("payrolldb: scan payslip for posting: %w", err)
		}
		// The employee ID and not the code or the name: the engine's
		// EmployeeRef is an opaque token it may print in an error, and this leaf
		// must never be handed an identifier that means anything to a reader
		// without the database.
		p.EmployeeRef = employeeID.String()
		p.Month = month
		p.SocialSecurityRuleSetVersion = ssVersion
		p.IncomeTaxRuleSetVersion = itVersion
		if payable != nil {
			p.PayableLedger = payable.String()
			ledgerIDs[p.PayableLedger] = *payable
		}
		slip, err := payroll.PayslipFromPersisted(p)
		if err != nil {
			return nil, fmt.Errorf("payrolldb: pay run %s: %w", payRunID, err)
		}
		slips = append(slips, slip)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("payrolldb: read payslips for posting: %w", err)
	}
	if len(slips) == 0 {
		return nil, fmt.Errorf("payrolldb: pay run %s has no payslips; a voucher with no postings fires NO balance check at all (posting_balanced is a trigger on the posting table), so it is refused here", payRunID)
	}

	// THE ONE LEG BUILDER. It decides every amount and every side, and it
	// refuses to return a set that does not balance to the paisa.
	legs, err := payroll.AggregateLegs(slips)
	if err != nil {
		return nil, fmt.Errorf("payrolldb: build the %s pay run's voucher legs: %w", month, err)
	}

	ps := make([]ledger.PostingInput, 0, len(legs.Legs))
	for _, l := range legs.Legs {
		id, ok := ledgerIDs[l.Ledger]
		if !ok {
			// Unreachable while the seven control ledgers resolve and every
			// payable ledger came off a row read above; asserted rather than
			// assumed, because a leg silently dropped here would post an
			// unbalanced voucher.
			return nil, fmt.Errorf("%w: %q is named by a pay-run posting leg but is not in this company's chart", ErrControlLedgerMissing, l.Ledger)
		}
		dc := ledger.Credit
		if l.Direction == payroll.DirectionDebit {
			dc = ledger.Debit
		}
		ps = append(ps, ledger.PostingInput{LedgerID: id, DrCr: dc, Amount: l.Amount})
	}
	return ps, nil
}

// ---------------------------------------------------------------------------
// Row writers and readers.
// ---------------------------------------------------------------------------

func insertPayslip(ctx context.Context, tx pgx.Tx, companyID, payRunID uuid.UUID, sl SlipInput) error {
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("new payslip id: %w", err)
	}
	deferrals, err := deferralsJSON(sl.Deferrals)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO payslip
		   (id, company_id, pay_run_id, employee_id, days_paid, lop_days,
		    gross_earnings, total_deductions, net_pay,
		    statutory_wages, epf_wages, eps_wages, edli_wages, esi_wages,
		    epf_employee_statutory, epf_employee_voluntary,
		    epf_employer_statutory, epf_employer_voluntary, eps_employer,
		    edli_contribution, edli_admin_charges, epf_admin_charges,
		    esi_employee, esi_employer, tds, deferrals)
		 VALUES ($1,$2,$3,$4,$5,$6,
		         $7,$8,$9,
		         $10,$11,$12,$13,$14,
		         $15,$16,
		         $17,$18,$19,
		         $20,$21,$22,
		         $23,$24,$25,$26)`,
		id, companyID, payRunID, sl.EmployeeID, sl.DaysPaid, sl.LOPDays,
		sl.GrossEarnings, sl.TotalDeductions, sl.NetPay,
		sl.StatutoryWages, sl.EPFWages, sl.EPSWages, sl.EDLIWages, sl.ESIWages,
		sl.EPFEmployeeStatutory, sl.EPFEmployeeVoluntary,
		sl.EPFEmployerStatutory, sl.EPFEmployerVoluntary, sl.EPSEmployer,
		sl.EDLIContribution, sl.EDLIAdminCharges, sl.EPFAdminCharges,
		sl.ESIEmployee, sl.ESIEmployer, sl.TDS, deferrals); err != nil {
		return fmt.Errorf("insert payslip: %w", err)
	}
	for i, c := range sl.Components {
		cid, cerr := uuid.NewV7()
		if cerr != nil {
			return fmt.Errorf("new payslip component id: %w", cerr)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO payslip_component
			   (id, company_id, payslip_id, line_no, component_code,
			    component_type, tag, direction, amount)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			cid, companyID, id, i+1, c.Code, c.Type,
			string(c.Tag), c.Direction, c.Amount); err != nil {
			return fmt.Errorf("insert payslip component %q: %w", c.Code, err)
		}
	}
	return nil
}

// deferralsJSON renders the engine's deferrals as the JSONB array the payslip
// column holds. A nil slice becomes `[]`, never `null`: the column's CHECK
// requires an array, and a null would make "no deferrals" and "nobody recorded
// whether there were deferrals" the same value.
func deferralsJSON(ds []payroll.Deferral) ([]byte, error) {
	if len(ds) == 0 {
		return []byte(`[]`), nil
	}
	out := make([]map[string]string, 0, len(ds))
	for _, d := range ds {
		out = append(out, map[string]string{
			"component": d.Component,
			"reason":    d.Reason,
			"citation":  d.Citation,
		})
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("payrolldb: render deferrals: %w", err)
	}
	return b, nil
}

func (s *Service) readResult(ctx context.Context, payRunID uuid.UUID, alreadyPosted bool) (PayRunResult, error) {
	res := PayRunResult{PayRunID: payRunID, AlreadyPosted: alreadyPosted}
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var vid *uuid.UUID
		if err := tx.QueryRow(ctx,
			`SELECT status, voucher_id FROM pay_run WHERE id = $1`, payRunID).
			Scan(&res.Status, &vid); err != nil {
			return err
		}
		if vid == nil {
			return nil
		}
		res.VoucherID = *vid
		var no *string
		if err := tx.QueryRow(ctx,
			`SELECT vv.voucher_no, vv.version_no
			   FROM voucher_head vh
			   JOIN voucher_version vv ON vv.id = vh.current_version_id
			  WHERE vh.voucher_id = $1`, res.VoucherID).Scan(&no, &res.VersionNo); err != nil {
			return err
		}
		if no != nil {
			res.VoucherNo = *no
		}
		return nil
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return res, nil
	case err != nil:
		return PayRunResult{}, fmt.Errorf("payrolldb: read pay run result: %w", err)
	}
	return res, nil
}

// LastDayOf returns the last day of the month t falls in. The pay run's voucher
// is dated the last day of the wage month: that is when the wages become
// payable, and it is what puts the entry in the right period on the P&L.
//
// IT IS EXPORTED BECAUSE THE PDP NEEDS THE SAME ANSWER. POST /vouchers/payroll
// carries no voucher_date on the wire — the date is derived here from
// wage_month — so internal/server's back-dated-cutoff peek has to derive it too,
// and it derives it by CALLING THIS. A second copy of the rule in the server
// package would be a second source of truth for one date: the cutoff would
// judge one day and the ledger would store another, and the drift would be
// invisible because nothing ever compares them.
//
// The body delegates to payroll.WageMonth.Last() for the same reason one level
// down: WageMonth.Last() is what the social-security rulesets resolve on, and
// the pay run's voucher must not be able to fall in a different month from the
// ruleset that priced it.
func LastDayOf(t time.Time) time.Time {
	return payroll.WageMonth{Year: t.Year(), Month: t.Month()}.Last()
}
