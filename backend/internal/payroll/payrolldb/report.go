package payrolldb

// report.go — the READ MODELS behind the payroll register and the payslip.
//
// Both are read-only projections over pay_run / payslip / payslip_component and
// the employee master. Neither writes anything, and neither may: a report that
// silently posts is how books stop matching returns.
//
// ACCESS. Migration 0035 writes EXPLICIT `disallow display report:payroll-register`
// and `report:payroll-payslip` rows on the seeded Data Entry level, and those
// rows are the ones that change an answer: migration 0011 gives that level a
// COARSE `allow display report` which matches EVERY report:NAME, so silence
// would have handed both of these to every operator in every tenant by
// inheritance. An Owner runs them, or grants them to a named payroll level.
//
// PII. The register carries names and net pay; the payslip carries one
// employee's whole figure. Neither returns the PAN, the UAN, the IP number or
// the bank details — those exist for the statutory FILES (payrollfile) and for
// nothing else, and a screen that prints them is a screen somebody will
// screenshot. RegisterRow and PayslipView are the closed list of what a reader
// gets.

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/payroll"
)

// RegisterRow is one employee's line on the payroll register for a wage month.
type RegisterRow struct {
	EmployeeID   uuid.UUID
	EmployeeCode string
	EmployeeName string
	DaysPaid     decimal.Decimal
	Gross        decimal.Decimal
	// The withheld heads, itemised. A register that showed only "deductions"
	// cannot answer the one question an employee ever asks about a deduction,
	// and it cannot be reconciled against the PF or ESI remittance either.
	EPFEmployee decimal.Decimal
	ESIEmployee decimal.Decimal
	TDS         decimal.Decimal
	Deductions  decimal.Decimal
	Net         decimal.Decimal
	// The employer's cost, which is NOT part of the employee's gross and must
	// never be added into it — it is a separate debit on the voucher.
	EPFEmployer decimal.Decimal
	EPSEmployer decimal.Decimal
	ESIEmployer decimal.Decimal
}

// Register is one wage month's register: the rows plus the run's identity and
// the rulesets it computed under.
type Register struct {
	PayRunID  uuid.UUID
	WageMonth string
	Status    string
	// The ruleset versions are ON THE REPORT, not just in the table. A payroll
	// figure with no statement of which law produced it cannot be checked by
	// anybody who was not present when it was run.
	SSRuleSetVersion string
	ITRuleSetVersion string
	Rows             []RegisterRow
	Totals           RegisterRow
}

// PayrollRegister returns the register for a wage month.
//
// It reads the LIVE run for the month — the partial unique index on
// (company_id, wage_month) WHERE status <> 'REVERSED' guarantees there is at
// most one — so a month that was reversed and re-run shows the corrected
// figures, while the reversed run's payslips remain in the table for anybody
// who asks for it by id.
func (s *Service) PayrollRegister(ctx context.Context, m payroll.WageMonth) (Register, error) {
	var reg Register
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		switch err := tx.QueryRow(ctx,
			`SELECT id, status, ss_ruleset_version, it_ruleset_version
			   FROM pay_run
			  WHERE wage_month = $1::date AND status <> $2`,
			m.First().Format("2006-01-02"), StatusReversed).
			Scan(&reg.PayRunID, &reg.Status, &reg.SSRuleSetVersion, &reg.ITRuleSetVersion); {
		case err == pgx.ErrNoRows:
			return fmt.Errorf("%w: no live pay run for %s", ErrPayRunNotFound, m)
		case err != nil:
			return err
		}
		reg.WageMonth = m.String()
		rows, err := tx.Query(ctx,
			`SELECT p.employee_id, e.code, e.name, p.days_paid,
			        p.gross_earnings,
			        p.epf_employee_statutory + p.epf_employee_voluntary,
			        p.esi_employee, p.tds, p.total_deductions, p.net_pay,
			        p.epf_employer_statutory + p.epf_employer_voluntary,
			        p.eps_employer, p.esi_employer
			   FROM payslip p
			   JOIN employee e ON e.company_id = p.company_id AND e.id = p.employee_id
			  WHERE p.pay_run_id = $1
			  ORDER BY e.code`, reg.PayRunID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r RegisterRow
			if err := rows.Scan(&r.EmployeeID, &r.EmployeeCode, &r.EmployeeName, &r.DaysPaid,
				&r.Gross, &r.EPFEmployee, &r.ESIEmployee, &r.TDS, &r.Deductions, &r.Net,
				&r.EPFEmployer, &r.EPSEmployer, &r.ESIEmployer); err != nil {
				return err
			}
			reg.Rows = append(reg.Rows, r)
			reg.Totals.Gross = reg.Totals.Gross.Add(r.Gross)
			reg.Totals.EPFEmployee = reg.Totals.EPFEmployee.Add(r.EPFEmployee)
			reg.Totals.ESIEmployee = reg.Totals.ESIEmployee.Add(r.ESIEmployee)
			reg.Totals.TDS = reg.Totals.TDS.Add(r.TDS)
			reg.Totals.Deductions = reg.Totals.Deductions.Add(r.Deductions)
			reg.Totals.Net = reg.Totals.Net.Add(r.Net)
			reg.Totals.EPFEmployer = reg.Totals.EPFEmployer.Add(r.EPFEmployer)
			reg.Totals.EPSEmployer = reg.Totals.EPSEmployer.Add(r.EPSEmployer)
			reg.Totals.ESIEmployer = reg.Totals.ESIEmployer.Add(r.ESIEmployer)
		}
		return rows.Err()
	})
	if err != nil {
		return Register{}, fmt.Errorf("payrolldb: payroll register for %s: %w", m, err)
	}
	return reg, nil
}

// PayslipLine is one line of a payslip as the report renders it.
type PayslipLine struct {
	Code      string
	Type      string
	Tag       payroll.ComponentTag
	Direction string
	Amount    decimal.Decimal
}

// PayslipView is one employee's payslip for one wage month.
type PayslipView struct {
	PayRunID     uuid.UUID
	WageMonth    string
	EmployeeID   uuid.UUID
	EmployeeCode string
	EmployeeName string
	DaysPaid     decimal.Decimal
	LOPDays      decimal.Decimal
	Gross        decimal.Decimal
	Deductions   decimal.Decimal
	Net          decimal.Decimal
	Lines        []PayslipLine
	// Deferrals is what the engine refused to compute for this employee — today
	// the EDLI contribution, whose rate the EDLI Scheme 2026 says will be
	// notified separately. It is ON THE PAYSLIP rather than in a log, because a
	// blank where a number should be is a question the employee will ask and the
	// honest answer is "the rate has not been notified", not "zero".
	Deferrals []payroll.Deferral
	// The ruleset versions this figure was produced under.
	SSRuleSetVersion string
	ITRuleSetVersion string
}

// Payslip returns one employee's payslip for a wage month.
func (s *Service) Payslip(ctx context.Context, m payroll.WageMonth, employeeID uuid.UUID) (PayslipView, error) {
	var v PayslipView
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var payslipID uuid.UUID
		var deferrals []byte
		switch err := tx.QueryRow(ctx,
			`SELECT p.id, p.pay_run_id, e.code, e.name, p.days_paid, p.lop_days,
			        p.gross_earnings, p.total_deductions, p.net_pay, p.deferrals,
			        r.ss_ruleset_version, r.it_ruleset_version
			   FROM payslip p
			   JOIN pay_run r ON r.company_id = p.company_id AND r.id = p.pay_run_id
			   JOIN employee e ON e.company_id = p.company_id AND e.id = p.employee_id
			  WHERE r.wage_month = $1::date AND r.status <> $2 AND p.employee_id = $3`,
			m.First().Format("2006-01-02"), StatusReversed, employeeID).
			Scan(&payslipID, &v.PayRunID, &v.EmployeeCode, &v.EmployeeName,
				&v.DaysPaid, &v.LOPDays, &v.Gross, &v.Deductions, &v.Net, &deferrals,
				&v.SSRuleSetVersion, &v.ITRuleSetVersion); {
		case err == pgx.ErrNoRows:
			return fmt.Errorf("%w: no live payslip for employee %s in %s", ErrPayRunNotFound, employeeID, m)
		case err != nil:
			return err
		}
		v.WageMonth = m.String()
		v.EmployeeID = employeeID
		if len(deferrals) > 0 {
			if err := json.Unmarshal(deferrals, &v.Deferrals); err != nil {
				return fmt.Errorf("decode deferrals: %w", err)
			}
		}
		rows, err := tx.Query(ctx,
			`SELECT component_code, component_type, tag, direction, amount
			   FROM payslip_component
			  WHERE payslip_id = $1 ORDER BY line_no`, payslipID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var l PayslipLine
			if err := rows.Scan(&l.Code, &l.Type, &l.Tag, &l.Direction, &l.Amount); err != nil {
				return err
			}
			v.Lines = append(v.Lines, l)
		}
		return rows.Err()
	})
	if err != nil {
		return PayslipView{}, fmt.Errorf("payrolldb: payslip: %w", err)
	}
	return v, nil
}
