package payrolldb

// compute.go — THE WIRE BETWEEN THE ENGINE AND THE BOOKS.
//
// ===========================================================================
// WHY THIS FILE EXISTS
// ===========================================================================
// Before it, internal/payroll was a LIBRARY AND NOT A FEATURE. ComputePayslip,
// StatutoryWages, PFDerive, ESICoverage, ESIDerive and IncomeTaxDerive had no
// non-test caller anywhere in the repository, while the function that actually
// creates a payslip — RunPayRun — accepted twenty-odd hand-suppliable money
// fields, checked two SELF-CONSISTENCY identities that hold for any internally
// consistent set of INVENTED numbers, wrote them to immutable tables and posted
// them to the ledger. Every statutory-correctness claim in the payroll track
// therefore rested on a code path nothing called.
//
// ComputeAndRunPayRun closes that gap from the front: it resolves the rulesets
// by wage month, reads the effective-dated salary structure and the statutory
// elections out of the database, calls payroll.ComputePayslip once per
// employee, and maps the result onto SlipInput. verifySlipsAgainstEngine closes
// it from the back — see payrun.go — by recomputing every slip at write time
// and refusing the run on any difference. Together they mean a pay run's
// numbers CANNOT be anything but the engine's.
//
// ===========================================================================
// THE ONE THING A CALLER STILL SUPPLIES, AND WHY IT IS NOT A HOLE
// ===========================================================================
// MonthInput carries FACTS THE DATABASE DOES NOT HOLD — attendance, the
// ex-s.192(2) particulars of a former employer, the employee's own Chapter VI-A
// and house-property declarations. Not one of them is a rupee of computed
// payroll: they are inputs the statute makes the employer take from somebody
// else, and no engine can derive them. Every derived figure — every wage base,
// every contribution, every rupee of tax — comes from internal/payroll and is
// re-derived at write time.
//
// The distinction is the whole security property of this file. If a field on
// MonthInput ever becomes something the engine could have computed, it belongs
// in the database and not here.
//
// ===========================================================================
// KNOWN GAPS. READ BEFORE THIS TOUCHES REAL BOOKS.
// ===========================================================================
// These are stated rather than papered over. Each one names the direction of
// the error, because the direction is what decides whether it is recoverable.
//
//   - NO pf_applicable COLUMN. employee_statutory_election (migration 0034)
//     has pf_base NOT NULL and no way to say "this employee is outside the EPF
//     Act at all". So an employee with an election in force is treated as a
//     provident-fund member. An employer genuinely outside the scheme cannot be
//     run through this path today. A schema change is needed; it is NOT
//     simulated with a sentinel pf_base, because a sentinel that means "no" in
//     a column whose CHECK says CEILING or ACTUAL is a bug waiting for a reader
//     that does not know about it.
//
//   - NO FIRST-PF-MEMBERSHIP DATE COLUMN. payroll.PFElection.FirstMemberOn is
//     the date the employee first became a provident-fund member WITH ANY
//     EMPLOYER, and it drives the EPS new-member exclusion. The employee master
//     holds only the date of joining THIS employer, which is a different fact,
//     and proxying one with the other would exclude from the pension scheme
//     people who belong in it. So it is a MonthInput field, and its zero value
//     means "first became a member before the cutoff" — the engine's own
//     default. DIRECTION OF ERROR: a high earner who first joined after the
//     cutoff and is not flagged has 8.33% of the pension base diverted to EPS
//     instead of staying in EPF. The employer's total cost is unchanged; the
//     money lands in the member's pension account rather than their provident
//     fund account.
//
//   - RESIDENCE AND AGE ARE NOT IN THE MASTER. NonResident and AgeBand are
//     MonthInput fields whose zero values are "resident" and "under 60" — the
//     overwhelming majority and, for the s.202 default (new) regime, the age
//     band has no effect at all. DIRECTION OF ERROR under the OLD regime: a
//     senior citizen who is not flagged gets the under-60 basic exemption,
//     which OVER-deducts and is refunded at assessment.
//
//   - TAXABLE SALARY IS TAKEN AS GROSS EARNINGS. The s.10 exemptions (HRA
//     under s.10(13A), leave travel concession) are NOT modelled, so an
//     old-regime employee's estimated annual taxable income is overstated by
//     whatever exemption they were entitled to. DIRECTION OF ERROR: OVER
//     deduction, recoverable by the employee as a refund. It is recorded as a
//     Deferral on every payslip computed under the OLD regime so that the
//     employee is told rather than left to discover it.
//
//   - PROJECTION IS FLAT. ProjectedRemainingTaxable is this month's gross times
//     the number of months after this one. A structure whose window ends before
//     the tax year does, or a known future revision, is not read into the
//     projection. s.392 re-estimates and re-spreads every month, so the error
//     self-corrects over the year rather than accumulating; the final month is
//     exact either way.
//
//   - NO PRORATION FOR A MID-MONTH REVISION. If a salary structure or an
//     election window opens or closes INSIDE the wage month, this file REFUSES
//     the run for that employee rather than picking the old figure or the new
//     one. Both answers are wrong without proration, and proration is not
//     modelled. A refusal that names the employee and the date is recoverable;
//     a silently halved (or doubled) month is not.
//
//   - NO PRORATION FOR A MID-MONTH JOINER OR LEAVER EITHER, and for the same
//     reason. An employee whose date of joining falls after the first of the
//     wage month, or whose date of leaving falls before its last day, has
//     worked PART of the month, and this file REFUSES them
//     (refuseMidMonthEmployment) rather than paying a whole month. Until that
//     refusal existed the whole month WAS paid: an 18,000 structure with a
//     date of leaving on the 5th produced gross 18,000, EPF 1,680, ESI 135 and
//     "31 days paid", so the payslip, the ECR and the ESI return all
//     over-stated the wage and TDS was deducted on a month that was never
//     earned. The employer's workaround is the same as for a revision: run the
//     part month outside this path. Marking the leaver inactive instead is NOT
//     a workaround — it withholds the final salary altogether, which is the
//     same defect with its sign flipped.
//
// ===========================================================================
// THE ESI-RECKONABLE WAGE — UNVERIFIED CLASSIFICATION
// ===========================================================================
// payroll.ESIElection.MonthWages is an INPUT to the engine and is deliberately
// NOT the Code on Social Security statutory wage: s.2(22) of the ESI Act, 1948
// and s.2(88) of the Code are different definitions, and feeding the PF base
// into ESI under-contributes for most employees while looking entirely
// plausible. Something has to map the seventeen ComponentTypes onto s.2(22),
// and this file is that something.
//
// THE MAPPING BELOW (esiExcluded / esiCoverageExcluded) IS UNVERIFIED. It was
// curated from secondary sources; the bare text of s.2(22) and of the ESIC's
// coverage circulars could not be retrieved from this environment. Treat it
// exactly as internal/payroll treats a ConfUnverified rate: re-derive it from
// the statute before this engine touches real books.
//
// The direction chosen when in doubt is INCLUSION, matching wages.go's reading
// of s.2(88): a shortfall of ESI contribution is recoverable from the employer
// with interest and damages under s.85B, while an over-contribution is a cost
// the employer bears and a benefit entitlement the employee keeps.

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/payroll"
	"github.com/khata/cored/internal/platform/db"
)

// isoDate is the one date layout this file parses and renders. Every date-like
// value in the payroll schema is a DATE column and every date in the engine is
// midnight UTC on a calendar date; the two meet here.
const isoDate = "2006-01-02"

// MonthInput is one employee's non-derivable facts for one wage month.
//
// EVERY FIELD IS A FACT SOMEBODY OUTSIDE THIS SYSTEM SUPPLIED — attendance, a
// former employer's particulars, the employee's own declarations. NOT ONE OF
// THEM IS A COMPUTED RUPEE. See the file header: the moment a field here
// becomes something the engine could derive, it belongs in the database.
//
// The zero value is a complete, usable input for the ordinary employee: a full
// month's attendance, a resident under 60, no former employer, no declarations.
// That is why the flags are phrased negatively (NonResident, not IsResident) —
// a zero-valued struct must not silently withdraw a relief.
type MonthInput struct {
	// EmployeeID names the employee. Required.
	EmployeeID uuid.UUID

	// PaidDays is the number of days wages were paid for. ZERO MEANS "the whole
	// month" and is resolved to the calendar length of the wage month; it is not
	// a literal zero, because a literal zero paid days is refused by ESIDerive
	// for a covered employee and would be an odd way to express a full month.
	//
	// It reaches Rule 52's low-wage exemption (through ESIInput.PaidDays) and
	// the ESI return's "No. of Days for which wages paid" column, so it is a
	// result of the run and not an annotation on it.
	PaidDays decimal.Decimal
	// LOPDays is loss-of-pay days. It is CARRIED, not applied: this file does
	// NOT reduce the month's remuneration by the loss-of-pay proportion, because
	// the schema records no per-employee working-day calendar and a proration
	// against a guessed denominator is a wrong salary. Supply the reduced
	// components in the salary structure, or supply nothing and accept that
	// LOPDays is reportage. The ECR's NCP-days column reads it.
	LOPDays decimal.Decimal

	// FirstPFMemberOn is the date the employee FIRST became a provident-fund
	// member with ANY employer, as an ISO date. EMPTY means "before the EPS
	// new-member cutoff", which is the engine's own zero-value meaning. See the
	// file header for why this is not proxied with the date of joining.
	FirstPFMemberOn string

	// PriorEmployerTaxable and PriorEmployerTDS are the ex-s.192(2) / Form 12B
	// particulars a mid-year joiner furnished about a FORMER employer. Ignoring
	// them is the commonest payroll-engine defect: the new employer computes on
	// a part-year salary, the slabs come out low, and the employee owes the
	// difference plus interest at assessment.
	PriorEmployerTaxable decimal.Decimal
	PriorEmployerTDS     decimal.Decimal

	// HousePropertyLoss is the loss under the head house property the employee
	// declared, as a POSITIVE number. It is the only ex-s.192(2B) reduction an
	// employer may give and it is capped by the engine.
	HousePropertyLoss decimal.Decimal
	// ChapterVIA is the aggregate Chapter VI-A deduction declared. OLD REGIME
	// ONLY — the engine ignores it under the new regime.
	ChapterVIA decimal.Decimal
	// OtherIncomeDeclared is income under other heads the employee reported so
	// it can be taken into account. It may only INCREASE the deduction.
	OtherIncomeDeclared decimal.Decimal

	// NonResident withdraws the age-band basic exemption and the s.156 rebate,
	// both of which are available to a RESIDENT individual only. Phrased
	// negatively so the zero value is the common case and not a silent
	// withdrawal.
	NonResident bool
	// AgeBand selects the OLD regime's basic exemption. Empty is
	// payroll.AgeBelow60. It has no effect under the new regime.
	AgeBand payroll.AgeBand
}

// ComputePayRunInput asks for a whole wage month to be computed from the
// database and posted.
type ComputePayRunInput struct {
	Month     payroll.WageMonth
	CreatedBy string
	Narration string
	// Inputs is one row per employee to run. EMPTY means every eligible
	// employee — active, joined on or before the month ended, and not left
	// before it began — each with a zero-valued MonthInput.
	//
	// A non-empty Inputs is also the SELECTION: an employee with no row is not
	// in the run, and a row naming an employee who is not eligible is an error
	// rather than a silent omission.
	Inputs []MonthInput
}

// ComputeAndRunPayRun computes a wage month from the database through
// internal/payroll and posts it.
//
// THIS IS THE ONLY WAY A PAY RUN SHOULD BE CREATED. RunPayRun still exists (a
// payroll operator computes, reviews the register, and posts, and the review
// step needs the figures to exist before the post) but it now re-derives every
// slip and refuses anything that is not the engine's output — so the two paths
// produce the same numbers or the second one produces none.
//
// The computed MonthInputs travel with the run, which is what lets the write
// path re-derive the SAME payslip: attendance and the employee's declarations
// are not in the database, so a verification that could not see them would
// refuse every run that used them.
func (s *Service) ComputeAndRunPayRun(ctx context.Context, in ComputePayRunInput) (PayRunResult, error) {
	slips, used, err := s.ComputeSlips(ctx, in.Month, in.Inputs)
	if err != nil {
		return PayRunResult{}, err
	}
	return s.RunPayRun(ctx, PayRunInput{
		Month:       in.Month,
		CreatedBy:   in.CreatedBy,
		Narration:   in.Narration,
		Slips:       slips,
		MonthInputs: used,
	})
}

// ComputeSlips computes one wage month's payslips from the database WITHOUT
// writing or posting anything.
//
// It returns the slips AND the resolved MonthInputs it used, in the same
// employee order, so a caller that passed the empty selection can hand both to
// RunPayRun and have the write path re-derive exactly what it was shown. A
// caller that discards the returned inputs and keeps only the slips will have
// its run refused the moment any employee had non-default attendance — which is
// the correct failure, because the run would otherwise post figures the write
// path could not reproduce.
func (s *Service) ComputeSlips(ctx context.Context, month payroll.WageMonth, inputs []MonthInput) ([]SlipInput, []MonthInput, error) {
	rulesets, err := ResolveRuleSets(month)
	if err != nil {
		return nil, nil, err
	}
	facts, order, err := s.loadPayrollFacts(ctx, month, inputs)
	if err != nil {
		return nil, nil, err
	}
	if len(order) == 0 {
		return nil, nil, fmt.Errorf("payrolldb: no employee is eligible for the %s pay run (active, joined on or before %s, and not left before %s); "+
			"a run with no payslips computes nothing and would post an empty voucher",
			month, month.Last().Format(isoDate), month.First().Format(isoDate))
	}

	supplied := map[uuid.UUID]MonthInput{}
	for _, mi := range inputs {
		supplied[mi.EmployeeID] = mi
	}

	slips := make([]SlipInput, 0, len(order))
	used := make([]MonthInput, 0, len(order))
	for _, id := range order {
		f := facts[id]
		mi := normalizeMonthInput(supplied[id], id, month)
		slip, err := computeOne(f, mi, month, rulesets)
		if err != nil {
			// The engine's sentinels say WHAT is wrong; this wrapper says WHO it
			// happened to. A payroll error is always about one person, and an
			// error message that does not name them is unactionable on a run of
			// two hundred.
			return nil, nil, fmt.Errorf("payrolldb: compute payslip for employee %s (%s), wage month %s: %w",
				f.Code, id, month, err)
		}
		slips = append(slips, slip)
		used = append(used, mi)
	}
	return slips, used, nil
}

// normalizeMonthInput resolves the zero-value meanings ONCE, so the compute
// path and the verification path cannot disagree about what a zero meant.
//
// That single-sourcing is not cosmetic: if "zero paid days means a full month"
// were applied on one side and not the other, every run for an employee who did
// not state attendance would compute one figure and be refused against another.
func normalizeMonthInput(mi MonthInput, id uuid.UUID, month payroll.WageMonth) MonthInput {
	mi.EmployeeID = id
	if mi.PaidDays.IsZero() {
		mi.PaidDays = decimal.NewFromInt(int64(month.Last().Day()))
	}
	if mi.AgeBand == "" {
		mi.AgeBand = payroll.AgeBelow60
	}
	return mi
}

// ---------------------------------------------------------------------------
// What the database holds.
// ---------------------------------------------------------------------------

// structureRow is one effective-dated salary component as stored, over a
// HALF-OPEN [From, To) window. A zero To is an open window.
type structureRow struct {
	From   time.Time
	To     time.Time
	Code   string
	Type   payroll.ComponentType
	Amount decimal.Decimal
}

// inForceOn reports whether the row's half-open window covers d.
func (r structureRow) inForceOn(d time.Time) bool {
	if r.From.After(d) {
		return false
	}
	return r.To.IsZero() || r.To.After(d)
}

// electionRow is one effective-dated statutory election as stored.
type electionRow struct {
	From                     time.Time
	To                       time.Time
	PFBase                   payroll.PFBase
	PFVoluntaryBase          decimal.Decimal
	EmployerMatchesVoluntary bool
	EPSMember                bool
	ESIApplicable            bool
	Regime                   payroll.Regime
}

// employeeFacts is everything the database knows about one employee that bears
// on one wage month.
type employeeFacts struct {
	ID           uuid.UUID
	Code         string
	DOJ          time.Time
	DOL          time.Time // zero = still employed
	PANAvailable bool

	// Structure is every salary-component row whose window touches the span
	// from the ESI contribution period's start to the end of the wage month.
	// The SPAN and not the month: the ESI coverage latch tests the wage in force
	// at the period start, which is a question about the past.
	Structure []structureRow
	Election  *electionRow

	// YTDTaxable and YTDTDS are this tax year's already-posted months with THIS
	// employer, excluding the month being computed. s.392 is annual, and an
	// engine that could not see the months already run would re-spread the whole
	// year's tax in every payslip.
	YTDTaxable decimal.Decimal
	YTDTDS     decimal.Decimal
}

// loadPayrollFacts reads the master, the effective-dated children and the
// year-to-date accumulation for a wage month, in ONE transaction.
//
// One transaction because RLS is keyed off the GUC RunInTx sets (see the
// package doc) and because a half-read set of facts is a half-computed payroll:
// reading the structure in one transaction and the year-to-date in another
// would let a concurrent pay run land between them and produce a payslip that
// never corresponded to any state of the database.
//
// It returns the facts by id and the deterministic ORDER (by employee code) the
// run is computed in. The order matters: it decides the order of the payslip
// rows and therefore the order of the per-employee net-pay legs, and a pay run
// whose voucher differs between two identical executions cannot be hash-chained
// meaningfully.
func (s *Service) loadPayrollFacts(ctx context.Context, month payroll.WageMonth, inputs []MonthInput) (map[uuid.UUID]*employeeFacts, []uuid.UUID, error) {
	if _, ok := db.CompanyFromContext(ctx); !ok {
		return nil, nil, db.ErrNoCompany
	}

	monthStart := month.First()
	monthEnd := month.Last()
	periodStart, _ := payroll.ContributionPeriodFor(monthStart)
	taxYearStart := month.TaxYear().Start()

	wanted := make([]uuid.UUID, 0, len(inputs))
	seen := map[uuid.UUID]bool{}
	for i, mi := range inputs {
		if mi.EmployeeID == uuid.Nil {
			return nil, nil, fmt.Errorf("payrolldb: month input %d has no employee id", i+1)
		}
		if seen[mi.EmployeeID] {
			// Two rows for one employee would silently make one of them win by
			// map order, and the two could carry different attendance.
			return nil, nil, fmt.Errorf("payrolldb: employee %s appears twice in the month inputs for %s", mi.EmployeeID, month)
		}
		seen[mi.EmployeeID] = true
		wanted = append(wanted, mi.EmployeeID)
	}

	facts := map[uuid.UUID]*employeeFacts{}
	var order []uuid.UUID

	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// (1) The master. Eligibility is EMPLOYMENT, tested against the month:
		// somebody who joined after the month ended, or left before it began,
		// has no wages for it.
		rows, err := tx.Query(ctx,
			`SELECT id, code, doj, dol, (pan IS NOT NULL AND pan <> '')
			   FROM employee
			  WHERE is_active
			    AND doj <= $1::date
			    AND (dol IS NULL OR dol >= $2::date)
			    AND ($3::uuid[] IS NULL OR id = ANY($3::uuid[]))
			  ORDER BY code`,
			monthEnd.Format(isoDate), monthStart.Format(isoDate), nullUUIDs(wanted))
		if err != nil {
			return fmt.Errorf("payrolldb: read employees for %s: %w", month, err)
		}
		defer rows.Close()
		for rows.Next() {
			f := &employeeFacts{}
			var dol *time.Time
			if err := rows.Scan(&f.ID, &f.Code, &f.DOJ, &dol, &f.PANAvailable); err != nil {
				return fmt.Errorf("payrolldb: scan employee: %w", err)
			}
			if dol != nil {
				f.DOL = dateOf(*dol)
			}
			f.DOJ = dateOf(f.DOJ)
			facts[f.ID] = f
			order = append(order, f.ID)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("payrolldb: read employees for %s: %w", month, err)
		}
		// A named employee who did not come back is an ERROR and never a silent
		// omission: the caller asked for a payslip for somebody and would
		// otherwise get a pay run that quietly did not include them.
		for _, id := range wanted {
			if facts[id] == nil {
				return fmt.Errorf("payrolldb: employee %s was named in the %s pay run but is not eligible for it "+
					"(they must be active, have joined on or before %s, and not have left before %s)",
					id, month, monthEnd.Format(isoDate), monthStart.Format(isoDate))
			}
		}
		if len(order) == 0 {
			return nil
		}
		ids := order

		// (2) The salary structure, back to the ESI contribution period's start.
		// The ESI coverage LATCH is tested at the period start (or at a
		// mid-period joiner's joining date), so a query bounded by the wage
		// month cannot implement it — it would silently degrade into a monthly
		// ceiling test, which is a different rule that covers different people.
		srows, err := tx.Query(ctx,
			`SELECT employee_id, effective_from, effective_to,
			        component_code, component_type, amount
			   FROM employee_salary_structure
			  WHERE employee_id = ANY($1)
			    AND effective_from <= $2::date
			    AND (effective_to IS NULL OR effective_to > $3::date)
			  ORDER BY employee_id, effective_from, component_code`,
			ids, monthEnd.Format(isoDate), periodStart.Format(isoDate))
		if err != nil {
			return fmt.Errorf("payrolldb: read salary structures for %s: %w", month, err)
		}
		defer srows.Close()
		for srows.Next() {
			var empID uuid.UUID
			var from time.Time
			var to *time.Time
			var code, ctype string
			var amount decimal.Decimal
			if err := srows.Scan(&empID, &from, &to, &code, &ctype, &amount); err != nil {
				return fmt.Errorf("payrolldb: scan salary component: %w", err)
			}
			// The vocabulary is resolved HERE and not trusted from the column.
			// The CHECK constraint and ComponentTypeFor are the same closed set
			// written twice on purpose; a value one side accepts and the other
			// refuses must be a loud error and not a silent exclusion from
			// wages.
			t, err := payroll.ComponentTypeFor(ctype)
			if err != nil {
				return fmt.Errorf("payrolldb: employee %s salary component %q: %w", empID, code, err)
			}
			r := structureRow{From: dateOf(from), Code: code, Type: t, Amount: amount}
			if to != nil {
				r.To = dateOf(*to)
			}
			facts[empID].Structure = append(facts[empID].Structure, r)
		}
		if err := srows.Err(); err != nil {
			return fmt.Errorf("payrolldb: read salary structures for %s: %w", month, err)
		}

		// (3) The statutory elections in force for the month.
		erows, err := tx.Query(ctx,
			`SELECT employee_id, effective_from, effective_to,
			        pf_base, COALESCE(pf_employee_voluntary_base, 0),
			        pf_employer_matches_voluntary, eps_member, esi_applicable,
			        tax_regime
			   FROM employee_statutory_election
			  WHERE employee_id = ANY($1)
			    AND effective_from <= $2::date
			    AND (effective_to IS NULL OR effective_to > $3::date)
			  ORDER BY employee_id, effective_from`,
			ids, monthEnd.Format(isoDate), monthStart.Format(isoDate))
		if err != nil {
			return fmt.Errorf("payrolldb: read statutory elections for %s: %w", month, err)
		}
		defer erows.Close()
		for erows.Next() {
			var empID uuid.UUID
			var from time.Time
			var to *time.Time
			var pfBase, regime string
			e := &electionRow{}
			if err := erows.Scan(&empID, &from, &to, &pfBase, &e.PFVoluntaryBase,
				&e.EmployerMatchesVoluntary, &e.EPSMember, &e.ESIApplicable, &regime); err != nil {
				return fmt.Errorf("payrolldb: scan statutory election: %w", err)
			}
			base, err := PFBaseOrDefault(pfBase)
			if err != nil {
				return fmt.Errorf("payrolldb: employee %s: %w", empID, err)
			}
			reg, err := RegimeOrDefault(regime)
			if err != nil {
				return fmt.Errorf("payrolldb: employee %s: %w", empID, err)
			}
			e.From = dateOf(from)
			if to != nil {
				e.To = dateOf(*to)
			}
			e.PFBase = base
			e.Regime = reg
			f := facts[empID]
			if f.Election != nil {
				// The EXCLUDE constraint forbids overlapping windows, so two
				// rows here mean one of them opens or closes inside the month.
				// Refuse rather than pick: an election that changed mid-month
				// needs proration this file does not model.
				return fmt.Errorf("payrolldb: employee %s has TWO statutory elections touching wage month %s (effective %s and %s); "+
					"an election that changes inside a wage month needs proration, which is not modelled — end the old window on the first of a month",
					empID, month, f.Election.From.Format(isoDate), e.From.Format(isoDate))
			}
			f.Election = e
		}
		if err := erows.Err(); err != nil {
			return fmt.Errorf("payrolldb: read statutory elections for %s: %w", month, err)
		}

		// (4) The year-to-date accumulation. s.392 estimates the WHOLE year's
		// tax and spreads what is left over the months remaining, so the months
		// already run are part of the input and not a report.
		//
		// REVERSED runs are excluded: their voucher was withdrawn, and counting
		// a withdrawn month's TDS as already credited would under-deduct for the
		// rest of the year by exactly that amount. The current month is excluded
		// by the strict upper bound — a re-run of a month must not see itself.
		yrows, err := tx.Query(ctx,
			`SELECT p.employee_id,
			        COALESCE(SUM(p.gross_earnings), 0), COALESCE(SUM(p.tds), 0)
			   FROM payslip p
			   JOIN pay_run r ON r.company_id = p.company_id AND r.id = p.pay_run_id
			  WHERE p.employee_id = ANY($1)
			    AND r.status <> $2
			    AND r.wage_month >= $3::date
			    AND r.wage_month <  $4::date
			  GROUP BY p.employee_id`,
			ids, StatusReversed, taxYearStart.Format(isoDate), monthStart.Format(isoDate))
		if err != nil {
			return fmt.Errorf("payrolldb: read year-to-date payroll for %s: %w", month, err)
		}
		defer yrows.Close()
		for yrows.Next() {
			var empID uuid.UUID
			var taxable, tds decimal.Decimal
			if err := yrows.Scan(&empID, &taxable, &tds); err != nil {
				return fmt.Errorf("payrolldb: scan year-to-date payroll: %w", err)
			}
			facts[empID].YTDTaxable = taxable
			facts[empID].YTDTDS = tds
		}
		return yrows.Err()
	})
	if err != nil {
		return nil, nil, err
	}
	return facts, order, nil
}

// nullUUIDs renders a selection as a nullable uuid[] parameter: nil means "no
// filter". A zero-length array would filter EVERYTHING out, which is the
// opposite of what an empty selection means here.
func nullUUIDs(ids []uuid.UUID) any {
	if len(ids) == 0 {
		return nil
	}
	return ids
}

// dateOf reduces a timestamp to midnight UTC on its calendar date, which is
// what every date in internal/payroll is and what every DATE column holds. A
// stray location or time-of-day would make an equality comparison against a
// window boundary depend on the driver's timezone handling.
func dateOf(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// ---------------------------------------------------------------------------
// The mapping. Database facts -> payroll.PayslipInput -> SlipInput.
// ---------------------------------------------------------------------------

// computeOne turns one employee's facts and one month's inputs into a
// persisted-shaped slip, through payroll.ComputePayslip.
//
// It is a pure function of its arguments — no context, no transaction, no
// clock — so the verification path in payrun.go can run it against the same
// facts and get the same answer, which is the whole basis of the refusal there.
func computeOne(f *employeeFacts, mi MonthInput, month payroll.WageMonth, rs RuleSets) (SlipInput, error) {
	if f.Election == nil {
		return SlipInput{}, fmt.Errorf("no statutory election is in force for wage month %s; "+
			"the PF contribution base and the tax regime are elections and there is no safe default for the first of them "+
			"(CEILING and ACTUAL differ for anyone paid above the wage ceiling)", month)
	}
	monthStart := month.First()
	monthEnd := month.Last()

	if err := refuseMidMonthEmployment(f, monthStart, monthEnd); err != nil {
		return SlipInput{}, err
	}

	if err := refuseMidMonthChange(f, monthStart, monthEnd); err != nil {
		return SlipInput{}, err
	}

	inForce := make([]structureRow, 0, len(f.Structure))
	for _, r := range f.Structure {
		if r.inForceOn(monthStart) {
			inForce = append(inForce, r)
		}
	}
	if len(inForce) == 0 {
		return SlipInput{}, fmt.Errorf("no salary structure is in force on %s; there is nothing to pay and nothing to contribute on",
			monthStart.Format(isoDate))
	}

	components := make([]payroll.WageComponent, 0, len(inForce))
	for _, r := range inForce {
		components = append(components, payroll.WageComponent{Type: r.Type, Amount: r.Amount})
	}

	firstMemberOn, err := parseOptionalDate(mi.FirstPFMemberOn, "first_pf_member_on")
	if err != nil {
		return SlipInput{}, err
	}

	revisions, err := esiRevisions(f, month)
	if err != nil {
		return SlipInput{}, err
	}

	// PaidDays reaches the engine as an INT because Rule 52's threshold is
	// limit x days and the ESI return's column is a whole number of days. A
	// fractional half-day is truncated DOWNWARD, which lowers the low-wage
	// threshold and therefore can only move an employee OUT of the exemption —
	// i.e. towards contributing. The other direction would withhold nothing from
	// somebody the statute says must contribute.
	paidDays := int(mi.PaidDays.IntPart())

	in := payroll.PayslipInput{
		// EmployeeRef is the row id and never a name, a PAN or a UAN: the engine
		// is kept unable to see an identifier it could print or log.
		EmployeeRef: f.ID.String(),
		Month:       month,
		Wage:        payroll.WageInput{Month: month, Components: components},
		PF: payroll.PFElection{
			// See the file header: migration 0034 has no pf_applicable column,
			// so an employee with an election in force is a provident-fund
			// member. This is the one place that assumption is made.
			Applicable:               true,
			Base:                     f.Election.PFBase,
			EmployeeVoluntaryBase:    f.Election.PFVoluntaryBase,
			EmployerMatchesVoluntary: f.Election.EmployerMatchesVoluntary,
			EPSMember:                f.Election.EPSMember,
			FirstMemberOn:            firstMemberOn,
		},
		ESI: payroll.ESIElection{
			Applicable: f.Election.ESIApplicable,
			JoinedOn:   f.DOJ,
			LeftOn:     f.DOL,
			Revisions:  revisions,
			MonthWages: esiMonthWages(inForce),
			PaidDays:   paidDays,
		},
		IncomeTax: incomeTaxInput(f, mi, month, inForce),
	}
	// PayableLedger stays EMPTY here on purpose. The engine's per-employee
	// payable is a LEDGER NAME, and payrolldb resolves the split from
	// employee.payable_ledger_id at posting time (buildPostings) — where it can
	// use the id and never has to trust a name. Setting it here would create a
	// second, drifting source for the same decision.

	slip, err := payroll.ComputePayslip(in, rs.SocialSecurity, rs.IncomeTax)
	if err != nil {
		return SlipInput{}, err
	}
	return toSlipInput(f, mi, slip, inForce)
}

// refuseMidMonthEmployment refuses an employee whose EMPLOYMENT — rather than
// their structure or their election — starts or ends inside the wage month.
//
// It is the sibling of refuseMidMonthChange and it exists because that function
// reads only effective-dated windows and never the master's dates, so a mid-
// month leaver whose structure is an open-ended row from an earlier month walked
// straight past it and was paid a WHOLE month. The observed figures for an
// 18,000 structure with a date of leaving on the 5th were gross 18,000, EPF
// 1,680, ESI 135 and 31 days paid — a payslip that states something untrue, an
// ECR and an ESI return that over-report the wage, and TDS deducted on a month
// that was not earned. A mid-month joiner is the same error in the other
// direction of cash flow but the same lie on the return; it was PARTLY caught
// already, but only by accident, when the joiner's structure happened to start
// on their date of joining (then the structure window, not the master, tripped
// the refusal). A joiner whose structure predates them was paid in full.
//
// The rule mirrors refuseMidMonthChange exactly: REFUSE, do not prorate.
// Proration is a policy choice — calendar days, working days, or the 26-day
// month the Payment of Wages practice uses — and this file cannot make it. A
// refusal naming the employee and the date is recoverable; a silently halved (or
// silently doubled) month is not.
//
// THE BOUNDARIES ARE NOT SYMMETRIC, because a date of joining and a date of
// leaving are not the same kind of date. Joining ON the first day of the month
// is a full month and is allowed; joining on any later day is not. Leaving ON
// the last day of the month is a full month and is allowed (DOL is the LAST
// WORKING DAY — see payroll.ESIElection.LeftOn); leaving on any earlier day,
// INCLUDING the first, is not. Getting these two backwards would either refuse
// every ordinary joiner and leaver or refuse none of them.
func refuseMidMonthEmployment(f *employeeFacts, monthStart, monthEnd time.Time) error {
	if !f.DOJ.IsZero() && f.DOJ.After(monthStart) && !f.DOJ.After(monthEnd) {
		return fmt.Errorf("employee %s joined on %s, inside the wage month; "+
			"a part-month of employment needs proration, which is not modelled — "+
			"pay the part month outside this run, or date the joining to the first of a month",
			f.Code, f.DOJ.Format(isoDate))
	}
	if !f.DOL.IsZero() && !f.DOL.Before(monthStart) && f.DOL.Before(monthEnd) {
		return fmt.Errorf("employee %s left on %s, inside the wage month; "+
			"a part-month of employment needs proration, which is not modelled — "+
			"pay the final part month outside this run, or date the leaving to the last day of a month",
			f.Code, f.DOL.Format(isoDate))
	}
	return nil
}

// refuseMidMonthChange refuses an employee whose salary structure or election
// window opens or closes INSIDE the wage month.
//
// Both available answers are wrong. Taking the old figure pays a whole month at
// a rate that stopped applying; taking the new one pays a whole month at a rate
// that had not started. The right answer is proration, this file does not model
// proration, and a refusal naming the employee and the date is recoverable
// where a silently halved month is not.
func refuseMidMonthChange(f *employeeFacts, monthStart, monthEnd time.Time) error {
	inside := func(d time.Time) bool {
		return !d.IsZero() && d.After(monthStart) && !d.After(monthEnd)
	}
	for _, r := range f.Structure {
		if inside(r.From) {
			return fmt.Errorf("salary component %q takes effect on %s, inside the wage month; "+
				"a mid-month revision needs proration, which is not modelled — date the revision to the first of a month",
				r.Code, r.From.Format(isoDate))
		}
		if inside(r.To) {
			return fmt.Errorf("salary component %q ends on %s, inside the wage month; "+
				"a mid-month change needs proration, which is not modelled — end the window on the first of a month",
				r.Code, r.To.Format(isoDate))
		}
	}
	if inside(f.Election.From) {
		return fmt.Errorf("the statutory election takes effect on %s, inside the wage month; "+
			"an election that changes inside a month needs proration, which is not modelled", f.Election.From.Format(isoDate))
	}
	if inside(f.Election.To) {
		return fmt.Errorf("the statutory election ends on %s, inside the wage month; "+
			"an election that changes inside a month needs proration, which is not modelled", f.Election.To.Format(isoDate))
	}
	return nil
}

// parseOptionalDate parses an ISO date, treating "" as the zero time. It
// REFUSES a malformed value rather than falling back to zero: the zero time
// carries a MEANING in the engine ("first became a member before the cutoff"),
// so a typo that silently became zero would silently change a statutory answer.
func parseOptionalDate(s, field string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	d, err := time.Parse(isoDate, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s %q is not an ISO date (YYYY-MM-DD)", field, s)
	}
	return dateOf(d), nil
}

// ---------------------------------------------------------------------------
// The ESI-reckonable wage. See the UNVERIFIED note in the file header.
// ---------------------------------------------------------------------------

// esiExcluded is s.2(22) of the ESI Act, 1948 as applied to this codebase's
// seventeen component types, for the CONTRIBUTION wage.
//
// It is a DENYLIST for the same reason wages.go's is: the statutory exclusion
// list is closed, so anything not on it is wages, and an allowlist would
// silently exclude every component type added later. HRA, special allowance and
// overtime are all ESI wages and are deliberately NOT here — HRA is excluded by
// s.2(88) of the Code (and therefore from the PF base) and included by s.2(22)
// of the ESI Act, and conflating the two definitions is the single commonest
// ESI under-contribution.
//
// UNVERIFIED. The four entries are the exclusions this mapping is most
// confident about; the classification of a Payment of Bonus Act bonus, of an
// award or settlement and of an ex gratia payment could not be settled against
// a primary text here, so they are treated as WAGES — the over-contributing
// direction. Re-derive before real books.
var esiExcluded = map[payroll.ComponentType]bool{
	// "any contribution paid by the employer to any pension fund or provident
	// fund, or under this Act"
	payroll.ComponentEmployerPFContribution: true,
	// "any travelling allowance or the value of any travelling concession"
	payroll.ComponentConveyanceAllowance: true,
	// "any sum paid to the person employed to defray special expenses entailed
	// on him by the nature of his employment"
	payroll.ComponentSpecialExpenses: true,
	// "any gratuity payable on discharge"
	payroll.ComponentGratuity: true,
}

// esiCoverageExcluded is the mapping used for the COVERAGE LATCH ONLY, and it
// differs from esiExcluded by exactly one member: OVERTIME.
//
// Overtime earnings are wages for CONTRIBUTION but are not reckoned when
// deciding whether an employee is covered — otherwise a month of heavy overtime
// would push somebody out of the scheme, and the scheme's own logic is that
// coverage latches for a whole contribution period precisely so it does not
// move with a month's earnings. Copying one map into the other is a bug in both
// places at once, which is why they are two maps and not one with a flag.
//
// UNVERIFIED, like esiExcluded.
var esiCoverageExcluded = map[payroll.ComponentType]bool{
	payroll.ComponentEmployerPFContribution: true,
	payroll.ComponentConveyanceAllowance:    true,
	payroll.ComponentSpecialExpenses:        true,
	payroll.ComponentGratuity:               true,
	payroll.ComponentOvertimeAllowance:      true,
}

// esiMonthWages sums the ESI-reckonable remuneration of the month.
func esiMonthWages(inForce []structureRow) decimal.Decimal {
	total := decimal.Zero
	for _, r := range inForce {
		if esiExcluded[r.Type] {
			continue
		}
		total = total.Add(r.Amount)
	}
	return total
}

// esiRevisions builds the effective-dated wage history the coverage LATCH is
// tested against.
//
// THE LATCH IS A QUESTION ABOUT THE PAST. ESICoverage tests the wage in force at
// the START of the contribution period (or at a mid-period joiner's joining
// date), in a pay run that may be executing five months later. A caller that
// could pass only the current wage cannot implement the latch at all and will
// silently implement a monthly ceiling test instead — which covers a different
// set of people and is invisible in the output.
//
// The history is built from the SAME salary structure the month's wages come
// from, evaluated at every date on which it changed inside the period. A
// revision is emitted only for a date on which some component was actually in
// force: emitting a synthetic zero for a date before the employee had any
// structure would make an uncovered employee look like a covered one earning
// nothing, and the engine's own "no wage revision in force" refusal is the
// honest answer instead.
func esiRevisions(f *employeeFacts, month payroll.WageMonth) ([]payroll.WageRevision, error) {
	periodStart, _ := payroll.ContributionPeriodFor(month.First())
	monthEnd := month.Last()

	dates := map[time.Time]bool{periodStart: true}
	add := func(d time.Time) {
		if d.IsZero() || d.Before(periodStart) || d.After(monthEnd) {
			return
		}
		dates[d] = true
	}
	for _, r := range f.Structure {
		add(r.From)
		add(r.To)
	}
	// A mid-period joiner is tested on the day they joined, so that day has to
	// be a candidate even when no component window happens to start on it.
	if !f.DOJ.IsZero() && f.DOJ.After(periodStart) && !f.DOJ.After(monthEnd) {
		dates[f.DOJ] = true
	}

	ordered := make([]time.Time, 0, len(dates))
	for d := range dates {
		ordered = append(ordered, d)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Before(ordered[j]) })

	var out []payroll.WageRevision
	var last decimal.Decimal
	haveLast := false
	for _, d := range ordered {
		wages := decimal.Zero
		any := false
		for _, r := range f.Structure {
			if !r.inForceOn(d) {
				continue
			}
			any = true
			if esiCoverageExcluded[r.Type] {
				continue
			}
			wages = wages.Add(r.Amount)
		}
		if !any {
			continue
		}
		// Consecutive dates that resolve to the same wage are ONE revision.
		// Emitting both would be harmless arithmetically but would put two rows
		// in the history where the employer made one change, and the ESI return
		// and any future audit read that history.
		if haveLast && wages.Equal(last) {
			continue
		}
		out = append(out, payroll.WageRevision{EffectiveFrom: d, MonthlyWages: wages})
		last = wages
		haveLast = true
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no salary structure was in force at any point between %s (the start of the ESI contribution period) and %s, "+
			"so the coverage latch has nothing to test",
			periodStart.Format(isoDate), monthEnd.Format(isoDate))
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Income tax.
// ---------------------------------------------------------------------------

// incomeTaxInput assembles the s.392 annual estimate for one month.
//
// The three figures that decide it, and why each is what it is:
//
//   - MonthsRemainingInclusive counts THIS month plus the months after it, to
//     the end of the tax year OR to the employee's last month, whichever comes
//     first. A leaver's remaining tax must not be spread over months that will
//     never happen, because the balance would then never be deducted at all.
//   - IsFinalMonth is March, or the month a leaver goes. The engine deducts the
//     WHOLE remaining balance with no division in that month, which is what
//     makes the year's deductions add up to the year's tax.
//   - ProjectedRemainingTaxable is this month's gross repeated over the months
//     after this one. See the file header for why the flat projection is
//     acceptable: s.392 re-estimates every month, so the error self-corrects and
//     the final month is exact either way.
func incomeTaxInput(f *employeeFacts, mi MonthInput, month payroll.WageMonth, inForce []structureRow) payroll.IncomeTaxInput {
	gross := decimal.Zero
	for _, r := range inForce {
		gross = gross.Add(r.Amount)
	}

	ty := month.TaxYear()
	lastMonth := payroll.WageMonth{Year: ty.End().Year(), Month: ty.End().Month()}
	if !f.DOL.IsZero() && !f.DOL.After(ty.End()) {
		lastMonth = payroll.WageMonth{Year: f.DOL.Year(), Month: f.DOL.Month()}
	}
	remaining := monthsBetweenInclusive(month, lastMonth)
	if remaining < 1 {
		// Only reachable for a leaver whose last working day precedes the month
		// being run, which the eligibility filter already excludes. Clamped to
		// one rather than left to divide by zero: the engine refuses anything
		// below one anyway (ErrMonthsRemaining), and this keeps the refusal in
		// the engine where the reason is documented.
		remaining = 1
	}

	after := decimal.NewFromInt(int64(remaining - 1))
	return payroll.IncomeTaxInput{
		TaxYear:      ty,
		Regime:       f.Election.Regime,
		IsResident:   !mi.NonResident,
		AgeBand:      mi.AgeBand,
		PANAvailable: f.PANAvailable,

		PriorEmployerTaxable: mi.PriorEmployerTaxable,
		PriorEmployerTDS:     mi.PriorEmployerTDS,

		YTDTaxableThisEmployer: f.YTDTaxable,
		YTDTDSThisEmployer:     f.YTDTDS,

		CurrentMonthTaxable:       gross,
		ProjectedRemainingTaxable: gross.Mul(after),

		MonthsRemainingInclusive: remaining,
		IsFinalMonth:             remaining == 1,

		HousePropertyLoss:   mi.HousePropertyLoss,
		ChapterVIA:          mi.ChapterVIA,
		OtherIncomeDeclared: mi.OtherIncomeDeclared,
	}
}

// monthsBetweenInclusive counts the wage months from a to b, both ends
// included. It is calendar arithmetic on (year, month) and never a duration:
// months are not a fixed number of days, and a duration-based count is off by
// one across February in a way that only shows up in one payslip a year.
func monthsBetweenInclusive(a, b payroll.WageMonth) int {
	return (b.Year-a.Year)*12 + int(b.Month) - int(a.Month) + 1
}

// ---------------------------------------------------------------------------
// payroll.Payslip -> SlipInput.
// ---------------------------------------------------------------------------

// toSlipInput maps the engine's payslip onto the row shape this package
// persists.
//
// IT IS A MAPPING AND NOT A SECOND COMPUTATION. Every figure below is read off
// the Payslip; nothing is re-derived, re-rounded or re-summed from parts. The
// one thing it does compute is the statutory/voluntary SPLIT of the employee's
// and employer's provident fund, and it reads that off the engine's own tagged
// COMPONENTS rather than subtracting totals — the components are what the ECR
// and Form V report, and a second derivation here could disagree with them.
func toSlipInput(f *employeeFacts, mi MonthInput, slip payroll.Payslip, inForce []structureRow) (SlipInput, error) {
	out := SlipInput{
		EmployeeID:      f.ID,
		DaysPaid:        mi.PaidDays,
		LOPDays:         mi.LOPDays,
		GrossEarnings:   slip.GrossEarnings,
		TotalDeductions: slip.EmployeeDeductions,
		NetPay:          slip.NetPay,
		StatutoryWages:  slip.Wage.StatutoryWage,
		TDS:             slip.Tax.TDSThisMonth,
		Deferrals:       slip.Deferrals,
	}

	if slip.PF != nil {
		out.EPFWages = slip.PF.EPFWages
		out.EPSWages = slip.PF.EPSWages
		out.EDLIWages = slip.PF.EDLIWages
		out.EPSEmployer = slip.PF.EPS
		// A CLOSED VOCABULARY WITH NO SILENT DEFAULT. Every component pf.go can
		// emit must land in a column here, because the pay run's voucher is
		// built from the COLUMNS: a component with nowhere to go is a statutory
		// liability that is computed, shown on no report and posted to no
		// ledger. That is exactly how the EDLI premium and the two
		// administrative charges went unbooked until migration 0036 gave them
		// their columns, and an unhandled name is now an error naming the
		// component rather than a silent zero.
		for _, c := range slip.PF.Components {
			switch c.Name {
			case payroll.ComponentEPFEmployee:
				out.EPFEmployeeStatutory = c.Amount
			case payroll.ComponentEPFEmployeeVoluntary:
				out.EPFEmployeeVoluntary = c.Amount
			case payroll.ComponentEPFEmployer:
				out.EPFEmployerStatutory = c.Amount
			case payroll.ComponentEPFEmployerVoluntary:
				out.EPFEmployerVoluntary = c.Amount
			case payroll.ComponentEPS:
				// Carried by PFResult.EPS and already read off the total above.
				// Reading it here as well would be a second source for one
				// figure.
			case payroll.ComponentEDLIContribution:
				out.EDLIContribution = c.Amount
			case payroll.ComponentEDLIAdminCharges:
				out.EDLIAdminCharges = c.Amount
			case payroll.ComponentEPFAdminCharges:
				out.EPFAdminCharges = c.Amount
			default:
				return SlipInput{}, fmt.Errorf(
					"payrolldb: provident-fund component %q has no column on the payslip, so it would be computed and then dropped: "+
						"add the column (a migration) and the mapping together, or the amount is credited to nobody and the EPFO challan will not reconcile",
					c.Name)
			}
		}
	}
	if slip.ESI != nil {
		out.ESIWages = slip.ESI.Wages
		out.ESIEmployee = slip.ESI.EmployeeShare
		out.ESIEmployer = slip.ESI.EmployerShare
	}

	// The payslip's own lines. Earnings first, in the structure's order (which
	// is ORDER BY effective_from, component_code — deterministic), then the
	// withheld heads in the order the payslip explains them.
	for _, r := range inForce {
		out.Components = append(out.Components, ComponentLine{
			Code: r.Code, Type: string(r.Type), Tag: payroll.TagNA,
			Direction: DirectionEarning, Amount: r.Amount,
		})
	}
	deductions := []struct {
		code   string
		tag    payroll.ComponentTag
		amount decimal.Decimal
	}{
		{payroll.ComponentEPFEmployee, payroll.TagStatutory, out.EPFEmployeeStatutory},
		{payroll.ComponentEPFEmployeeVoluntary, payroll.TagVoluntary, out.EPFEmployeeVoluntary},
		{payroll.ComponentESIEmployee, payroll.TagStatutory, out.ESIEmployee},
		{"TDS", payroll.TagStatutory, out.TDS},
	}
	for _, d := range deductions {
		// A zero line is omitted. "Deducted nothing" and "not applicable" are
		// different facts and a payslip showing "ESI 0.00" for somebody outside
		// the scheme states the wrong one; where the engine means "nil but
		// applicable" — the Rule 52 exemption — the ESI return carries it, and
		// the ESI wage on the payslip row is what says the employee was covered.
		if d.amount.IsZero() {
			continue
		}
		out.Components = append(out.Components, ComponentLine{
			Code: d.code, Type: d.code, Tag: d.tag,
			Direction: DirectionDeduction, Amount: d.amount,
		})
	}

	// The OLD-regime disclosure. See the file header: the s.10 exemptions are
	// not modelled, so an old-regime estimate is high and the deduction with it.
	// It travels ON THE PAYSLIP because the employee is the person the money was
	// taken from, and a number they cannot explain is one they cannot query.
	if slip.Tax.RegimeApplied == payroll.RegimeOld {
		out.Deferrals = append(out.Deferrals, payroll.Deferral{
			Component: "SECTION_10_EXEMPTIONS",
			Reason: "house rent allowance under s.10(13A) and leave travel concession are not computed by this engine, " +
				"so the estimated annual taxable salary is HIGH by whatever exemption is due and the monthly deduction is high with it; " +
				"the excess is refundable on assessment",
			Citation: "Income-tax Act 2025 s.10(13A) and the corresponding rule 2A (UNVERIFIED: not read against a primary text here)",
		})
	}
	return out, nil
}
