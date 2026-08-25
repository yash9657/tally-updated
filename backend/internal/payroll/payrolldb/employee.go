package payrolldb

// employee.go — the employee master, its effective-dated salary structure and
// statutory elections, and the INPUT VALIDATION that keeps identifiers honest.
//
// ===========================================================================
// WHY THE VALIDATION IS GO-NATIVE AND NOT REUSED
// ===========================================================================
// insightd carries PAN/Aadhaar recognizers already, and they cannot be reused
// here: they are Python, they live in a different deployable plane, and their
// job is REDACTION on an outbound cross-border LLM call. This is INPUT
// validation on the write path of the master itself, which is a different
// question asked at a different time. So the four patterns are written again,
// in Go, next to the INSERT they guard.
//
// ===========================================================================
// THE VERHOEFF CHECK IS A NEGATIVE CONTROL, WHICH IS UNUSUAL AND DELIBERATE
// ===========================================================================
// Migration 0034 stores no Aadhaar and never will: no statutory output in scope
// needs one (Form 138 needs the PAN, the ECR needs the UAN, the ESI return
// needs the IP number), and there is no at-rest protection in this plane to
// store one behind.
//
// Absence of a column is not, by itself, absence of the data. The realistic
// leak is somebody typing an Aadhaar into `designation` or `code` because there
// was nowhere else to put it, and the value then sits in plaintext in a table
// with no special handling, gets read by the register, and is exported to CSV.
//
// So the Verhoeff checksum — the algorithm the UIDAI number carries — is
// implemented here for the SOLE purpose of REFUSING a value that passes it. It
// is the only checksum validator in this codebase whose success is an error.
// Length alone would be far too broad (a twelve-digit staff number is a
// perfectly ordinary thing); the checksum is what makes the refusal specific
// enough to be worth having. TestEmployeeRejectsAadhaarShapedInput pins it with
// a genuinely Verhoeff-valid number.

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/payroll"
	"github.com/khata/cored/internal/platform/db"
)

// ErrInvalidEmployeeField is returned when a master field fails its format
// check. It is a single sentinel so the HTTP layer can map the whole class to
// 400 without enumerating the fields; the wrapped message names the field.
var ErrInvalidEmployeeField = errors.New("payrolldb: invalid employee field")

// ErrAadhaarShapedValue is returned when a free-text employee field carries a
// value that is twelve digits AND passes the Verhoeff checksum — i.e. almost
// certainly an Aadhaar number. See the file header: this database deliberately
// has nowhere to put one, and the refusal is what keeps "nowhere to put it"
// from becoming "in the designation column".
var ErrAadhaarShapedValue = errors.New(
	"payrolldb: this value is an Aadhaar number (twelve digits, valid Verhoeff checksum) and this system deliberately does not store Aadhaar: " +
		"no statutory output in scope needs it (Form 138 needs the PAN, the PF ECR the UAN, the ESI return the IP number) and there is no at-rest protection here to store it behind")

// The four identifier formats. Each is a shipped statutory output's
// requirement, not a general-purpose validator:
//
//	PAN   — Form 138 / Form 130 deductee records. Five letters, four digits, one
//	        letter. The fourth character encodes holder type and the fifth the
//	        surname initial, which is why the whole shape is checkable at all.
//	IFSC  — the bank branch code: four letters, a literal '0' (reserved), then
//	        six alphanumerics. The zero is the part people get wrong, and an
//	        IFSC with a letter in that position is not a bank branch.
//	UAN   — the EPFO Universal Account Number, twelve digits.
//	IP no — the ESIC Insured Person number, ten digits.
var (
	panRe  = regexp.MustCompile(`^[A-Z]{5}[0-9]{4}[A-Z]$`)
	ifscRe = regexp.MustCompile(`^[A-Z]{4}0[A-Z0-9]{6}$`)
	uanRe  = regexp.MustCompile(`^[0-9]{12}$`)
	ipRe   = regexp.MustCompile(`^[0-9]{10}$`)
	// last4Re guards the ONE bank field this schema keeps. The full account
	// number is deliberately not stored: v1 emits no bank advice file, so it
	// would be pure exposure, and the at-rest encryption decision falls due when
	// that file ships rather than now.
	last4Re  = regexp.MustCompile(`^[0-9]{4}$`)
	digitsRe = regexp.MustCompile(`^[0-9]{12}$`)
)

// Employee is the master row as this package reads and writes it.
//
// Every identifier here is present because a shipped statutory output needs it
// and for no other reason, and NONE of them ever reaches internal/payroll — the
// engine takes `PANAvailable bool`, so the pure leaf cannot print, log or hash
// a real identifier even by accident.
type Employee struct {
	ID          uuid.UUID
	Code        string
	Name        string
	DOJ         string // ISO date
	DOL         string // ISO date; empty = still employed
	Designation string
	PAN         string
	UAN         string
	ESICIPNo    string
	BankIFSC    string
	// BankAccountLast4 is FOUR DIGITS. See last4Re.
	BankAccountLast4 string
	// PayableLedgerID is the OPT-IN per-employee payable. uuid.Nil (the default)
	// routes this employee's net pay into the aggregate Salary Payable control
	// ledger, so their individual figure never reaches the Day Book.
	PayableLedgerID uuid.UUID
	IsActive        bool
}

// freeTextFields returns the fields an Aadhaar could be smuggled into, with
// their names, so the scan below reports WHICH field carried it.
//
// PAN/UAN/IFSC are excluded not because they are safe but because their own
// format checks already refuse a twelve-digit string — except UAN, which IS
// twelve digits and is therefore checked explicitly below with a different
// rule (see validate).
func (e Employee) freeTextFields() []struct{ name, value string } {
	return []struct{ name, value string }{
		{"code", e.Code},
		{"name", e.Name},
		{"designation", e.Designation},
	}
}

// Validate checks the master's formats and refuses an Aadhaar-shaped value.
//
// It runs BEFORE the transaction opens, like every other structural check in
// this codebase, so a request that cannot possibly be right fails with a domain
// message naming the field instead of arriving as a constraint violation with a
// constraint name in it.
func (e Employee) Validate() error {
	if strings.TrimSpace(e.Code) == "" {
		return fmt.Errorf("%w: code is required (it is what appears on the payslip)", ErrInvalidEmployeeField)
	}
	if strings.TrimSpace(e.Name) == "" {
		return fmt.Errorf("%w: name is required", ErrInvalidEmployeeField)
	}
	if _, err := time.Parse("2006-01-02", e.DOJ); err != nil {
		return fmt.Errorf("%w: doj %q is not an ISO date (YYYY-MM-DD)", ErrInvalidEmployeeField, e.DOJ)
	}
	if e.DOL != "" {
		if _, err := time.Parse("2006-01-02", e.DOL); err != nil {
			return fmt.Errorf("%w: dol %q is not an ISO date (YYYY-MM-DD)", ErrInvalidEmployeeField, e.DOL)
		}
	}
	// Each identifier is OPTIONAL — an employee below the PF and ESI thresholds
	// genuinely has neither a UAN nor an IP number, and refusing to record them
	// at all would be worse than recording them without one — but a value that
	// IS present must be the right shape, because a malformed PAN on a Form 138
	// is a deductee record the department rejects and a malformed IFSC is a
	// salary paid to nobody.
	if e.PAN != "" && !panRe.MatchString(e.PAN) {
		return fmt.Errorf("%w: pan %q is not a PAN (five letters, four digits, one letter); a malformed PAN is a Form 138 deductee record the department rejects", ErrInvalidEmployeeField, e.PAN)
	}
	if e.BankIFSC != "" && !ifscRe.MatchString(e.BankIFSC) {
		return fmt.Errorf("%w: bank_ifsc %q is not an IFSC (four letters, a literal 0, then six alphanumerics); the fifth character is reserved and is always zero", ErrInvalidEmployeeField, e.BankIFSC)
	}
	if e.UAN != "" && !uanRe.MatchString(e.UAN) {
		return fmt.Errorf("%w: uan %q is not a UAN (twelve digits)", ErrInvalidEmployeeField, e.UAN)
	}
	if e.ESICIPNo != "" && !ipRe.MatchString(e.ESICIPNo) {
		return fmt.Errorf("%w: esic_ip_number %q is not an ESIC IP number (ten digits)", ErrInvalidEmployeeField, e.ESICIPNo)
	}
	if e.BankAccountLast4 != "" && !last4Re.MatchString(e.BankAccountLast4) {
		return fmt.Errorf("%w: bank_account_last4 %q must be exactly four digits; the FULL account number is deliberately not stored", ErrInvalidEmployeeField, e.BankAccountLast4)
	}

	// THE NEGATIVE CONTROL. See the file header.
	for _, f := range e.freeTextFields() {
		if IsAadhaarShaped(f.value) {
			return fmt.Errorf("%w (field %q)", ErrAadhaarShapedValue, f.name)
		}
	}
	// The UAN is twelve digits too, so a Verhoeff-valid Aadhaar typed into it
	// would pass uanRe. A real UAN passing Verhoeff by chance is a 1-in-10
	// coincidence, so this cannot be a hard refusal without rejecting one UAN in
	// ten — it is deliberately NOT checked, and that gap is written down here
	// rather than papered over. The free-text fields are where the realistic
	// mistake lands, and they are covered.
	return nil
}

// IsAadhaarShaped reports whether s is twelve digits AND passes the Verhoeff
// checksum — i.e. is almost certainly an Aadhaar number.
//
// Exported so the eventual HTTP layer can refuse one at the edge with the same
// answer this package gives, rather than growing a second, drifting copy.
func IsAadhaarShaped(s string) bool {
	t := strings.TrimSpace(s)
	if !digitsRe.MatchString(t) {
		return false
	}
	return verhoeffValid(t)
}

// ---------------------------------------------------------------------------
// Verhoeff. Ported from the published algorithm (Verhoeff, 1969), which is what
// the UIDAI number's twelfth digit is.
//
// The three tables ARE the algorithm; there is no shorter honest way to write
// it. d is the multiplication table of the dihedral group D5, p is the
// permutation applied at each position, and inv is the inverse in D5. A
// transcription error in any of them would make this accept and reject the
// wrong numbers, so TestVerhoeffAgainstKnownVectors checks it against numbers
// whose validity is known independently — including single-digit and
// adjacent-transposition mutants, which are exactly the errors Verhoeff was
// designed to catch and therefore the ones that prove the tables are right.
// ---------------------------------------------------------------------------

var verhoeffD = [10][10]int{
	{0, 1, 2, 3, 4, 5, 6, 7, 8, 9},
	{1, 2, 3, 4, 0, 6, 7, 8, 9, 5},
	{2, 3, 4, 0, 1, 7, 8, 9, 5, 6},
	{3, 4, 0, 1, 2, 8, 9, 5, 6, 7},
	{4, 0, 1, 2, 3, 9, 5, 6, 7, 8},
	{5, 9, 8, 7, 6, 0, 4, 3, 2, 1},
	{6, 5, 9, 8, 7, 1, 0, 4, 3, 2},
	{7, 6, 5, 9, 8, 2, 1, 0, 4, 3},
	{8, 7, 6, 5, 9, 3, 2, 1, 0, 4},
	{9, 8, 7, 6, 5, 4, 3, 2, 1, 0},
}

var verhoeffP = [8][10]int{
	{0, 1, 2, 3, 4, 5, 6, 7, 8, 9},
	{1, 5, 7, 6, 2, 8, 3, 0, 9, 4},
	{5, 8, 0, 3, 7, 9, 6, 1, 4, 2},
	{8, 9, 1, 6, 0, 4, 3, 5, 2, 7},
	{9, 4, 5, 3, 1, 2, 6, 8, 7, 0},
	{4, 2, 8, 6, 5, 7, 3, 9, 0, 1},
	{2, 7, 9, 3, 8, 0, 6, 4, 1, 5},
	{7, 0, 4, 6, 9, 1, 3, 2, 5, 8},
}

// verhoeffValid reports whether a digit string carries a valid Verhoeff check
// digit as its LAST character.
func verhoeffValid(digits string) bool {
	c := 0
	// The digits are consumed RIGHT TO LEFT, with the check digit at position 0.
	for i, n := 0, len(digits); i < n; i++ {
		d := int(digits[n-1-i] - '0')
		c = verhoeffD[c][verhoeffP[i%8][d]]
	}
	return c == 0
}

// ---------------------------------------------------------------------------
// Master writes. Every one goes through RunInTx, so RLS confines it to the
// caller's company and the WITH CHECK half of the tenant_isolation policy
// refuses a row written for anybody else.
// ---------------------------------------------------------------------------

// CreateEmployee validates and inserts an employee master row, returning its id.
func (s *Service) CreateEmployee(ctx context.Context, e Employee) (uuid.UUID, error) {
	if err := e.Validate(); err != nil {
		return uuid.Nil, err
	}
	if e.ID == uuid.Nil {
		id, err := uuid.NewV7()
		if err != nil {
			return uuid.Nil, fmt.Errorf("payrolldb: new employee id: %w", err)
		}
		e.ID = id
	}
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return uuid.Nil, db.ErrNoCompany
	}
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO employee
			   (id, company_id, code, name, doj, dol, designation,
			    pan, uan, esic_ip_number, bank_ifsc, bank_account_last4,
			    payable_ledger_id, is_active)
			 VALUES ($1, $2, $3, $4, $5::date, $6, $7,
			         $8, $9, $10, $11, $12,
			         $13, $14)`,
			e.ID, companyID, e.Code, e.Name, e.DOJ, nullDate(e.DOL), e.Designation,
			nullText(e.PAN), nullText(e.UAN), nullText(e.ESICIPNo),
			nullText(e.BankIFSC), nullText(e.BankAccountLast4),
			nullUUID(e.PayableLedgerID), e.IsActive)
		return err
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("payrolldb: create employee %q: %w", e.Code, err)
	}
	return e.ID, nil
}

// AmendEmployee restates the mutable master fields of an existing employee.
//
// The master is NOT immutable (a payslip is; a master is a current statement of
// fact and an employee's IFSC legitimately changes when they change banks).
// What is immutable is the RESULT: correcting a master does not restate a
// payslip that was already produced from it, which is why a correction that
// must change money reverses the pay run and re-runs the month.
func (s *Service) AmendEmployee(ctx context.Context, e Employee) error {
	if e.ID == uuid.Nil {
		return fmt.Errorf("%w: employee id is required to amend", ErrInvalidEmployeeField)
	}
	if err := e.Validate(); err != nil {
		return err
	}
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		ct, err := tx.Exec(ctx,
			`UPDATE employee
			    SET code = $2, name = $3, doj = $4::date, dol = $5, designation = $6,
			        pan = $7, uan = $8, esic_ip_number = $9,
			        bank_ifsc = $10, bank_account_last4 = $11,
			        payable_ledger_id = $12, is_active = $13
			  WHERE id = $1`,
			e.ID, e.Code, e.Name, e.DOJ, nullDate(e.DOL), e.Designation,
			nullText(e.PAN), nullText(e.UAN), nullText(e.ESICIPNo),
			nullText(e.BankIFSC), nullText(e.BankAccountLast4),
			nullUUID(e.PayableLedgerID), e.IsActive)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			// RLS makes another tenant's employee INVISIBLE rather than
			// forbidden, so "no rows" and "not yours" are the same observation
			// here — which is the correct answer to give either way.
			return fmt.Errorf("payrolldb: no employee %s in this company", e.ID)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("payrolldb: amend employee %s: %w", e.ID, err)
	}
	return nil
}

// ListEmployees returns the company's employees, active first then by code.
func (s *Service) ListEmployees(ctx context.Context, includeInactive bool) ([]Employee, error) {
	var out []Employee
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT id, code, name, doj::text, COALESCE(dol::text, ''), designation,
			        COALESCE(pan, ''), COALESCE(uan, ''), COALESCE(esic_ip_number, ''),
			        COALESCE(bank_ifsc, ''), COALESCE(bank_account_last4, ''),
			        payable_ledger_id, is_active
			   FROM employee
			  WHERE ($1 OR is_active)
			  ORDER BY is_active DESC, code`, includeInactive)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var e Employee
			var payable *uuid.UUID
			if err := rows.Scan(&e.ID, &e.Code, &e.Name, &e.DOJ, &e.DOL, &e.Designation,
				&e.PAN, &e.UAN, &e.ESICIPNo, &e.BankIFSC, &e.BankAccountLast4,
				&payable, &e.IsActive); err != nil {
				return err
			}
			if payable != nil {
				e.PayableLedgerID = *payable
			}
			out = append(out, e)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("payrolldb: list employees: %w", err)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// The effective-dated children.
// ---------------------------------------------------------------------------

// SalaryComponent is one earning, in force over a HALF-OPEN
// [EffectiveFrom, EffectiveTo) window (empty EffectiveTo = still in force).
//
// Half-open, copying migration 0030's price-list convention: with an inclusive
// upper bound the day of a revision belongs to BOTH the old row and the new
// one, and a pay run for that month would have two answers to "what is the
// basic". The database enforces it with an EXCLUDE constraint, so the second
// overlapping row is refused rather than silently preferred by whatever ORDER
// BY the reader happened to use.
type SalaryComponent struct {
	ID            uuid.UUID
	EmployeeID    uuid.UUID
	EffectiveFrom string
	EffectiveTo   string
	Code          string
	Type          payroll.ComponentType
	Amount        decimal.Decimal
}

// SetSalaryComponent inserts one effective-dated salary component.
//
// The component TYPE is resolved through payroll.ComponentTypeFor rather than
// passed through as a string, so a type nobody implemented is refused HERE
// (with the vocabulary named) rather than at the CHECK constraint. The two are
// the same closed set written twice on purpose: s.2(88) of the Code on Social
// Security lists the exclusions from "wages" exhaustively, so a component that
// silently fell through to "excluded" would under-contribute PF and ESI for the
// whole of that employee's service with no error anywhere.
func (s *Service) SetSalaryComponent(ctx context.Context, c SalaryComponent) (uuid.UUID, error) {
	if _, err := payroll.ComponentTypeFor(string(c.Type)); err != nil {
		return uuid.Nil, fmt.Errorf("payrolldb: salary component %q: %w", c.Code, err)
	}
	if c.Amount.IsNegative() {
		return uuid.Nil, fmt.Errorf("%w: salary component %q amount is negative (%s); a negative earning is a deduction keyed into the wrong column and would flow straight through the PF base",
			ErrInvalidEmployeeField, c.Code, c.Amount.StringFixed(money))
	}
	if c.ID == uuid.Nil {
		id, err := uuid.NewV7()
		if err != nil {
			return uuid.Nil, fmt.Errorf("payrolldb: new salary component id: %w", err)
		}
		c.ID = id
	}
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return uuid.Nil, db.ErrNoCompany
	}
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO employee_salary_structure
			   (id, company_id, employee_id, effective_from, effective_to,
			    component_code, component_type, amount)
			 VALUES ($1, $2, $3, $4::date, $5, $6, $7, $8)`,
			c.ID, companyID, c.EmployeeID, c.EffectiveFrom, nullDate(c.EffectiveTo),
			c.Code, string(c.Type), c.Amount)
		return err
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("payrolldb: set salary component %q: %w", c.Code, err)
	}
	return c.ID, nil
}

// StatutoryElection is one employee's PF/EPS/ESI/regime choices over a
// HALF-OPEN [EffectiveFrom, EffectiveTo) window.
type StatutoryElection struct {
	ID            uuid.UUID
	EmployeeID    uuid.UUID
	EffectiveFrom string
	EffectiveTo   string
	PFBase        payroll.PFBase
	// PFVoluntaryBase is a voluntary provident fund base ABOVE the statutory
	// one; zero means none.
	PFVoluntaryBase        decimal.Decimal
	EmployerMatchVoluntary bool
	EPSMember              bool
	ESIApplicable          bool
	// Regime empty means "no election", which RegimeOrDefault resolves to the
	// s.202 default (NEW). It is stored as given so a later reader can still
	// tell an election from a non-election.
	Regime payroll.Regime
}

// SetStatutoryElection inserts one effective-dated election.
func (s *Service) SetStatutoryElection(ctx context.Context, e StatutoryElection) (uuid.UUID, error) {
	base, err := PFBaseOrDefault(string(e.PFBase))
	if err != nil {
		return uuid.Nil, err
	}
	regime, err := RegimeOrDefault(string(e.Regime))
	if err != nil {
		return uuid.Nil, err
	}
	if e.ID == uuid.Nil {
		id, nerr := uuid.NewV7()
		if nerr != nil {
			return uuid.Nil, fmt.Errorf("payrolldb: new election id: %w", nerr)
		}
		e.ID = id
	}
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return uuid.Nil, db.ErrNoCompany
	}
	var voluntary any
	if e.PFVoluntaryBase.IsPositive() {
		voluntary = e.PFVoluntaryBase
	}
	err = s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, ierr := tx.Exec(ctx,
			`INSERT INTO employee_statutory_election
			   (id, company_id, employee_id, effective_from, effective_to,
			    pf_base, pf_employee_voluntary_base, pf_employer_matches_voluntary,
			    eps_member, esi_applicable, tax_regime)
			 VALUES ($1, $2, $3, $4::date, $5, $6, $7, $8, $9, $10, $11)`,
			e.ID, companyID, e.EmployeeID, e.EffectiveFrom, nullDate(e.EffectiveTo),
			string(base), voluntary, e.EmployerMatchVoluntary,
			e.EPSMember, e.ESIApplicable, string(regime))
		return ierr
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("payrolldb: set statutory election: %w", err)
	}
	return e.ID, nil
}

// ---------------------------------------------------------------------------
// Reading the effective-dated children back.
//
// ⛔ BOTH READERS EXIST BECAUSE THE WRITERS ARE NOT ENOUGH TO BE USABLE. The pay
// run refuses an employee with no salary structure in force ("there is nothing
// to pay and nothing to contribute on") and refuses one with no statutory
// election ("the PF contribution base and the tax regime are elections and there
// is no safe default"), and both refusals arrive as a 422 on the RUN — long
// after the employee was created and with nothing anywhere saying the master is
// incomplete. A screen that cannot read these back can only discover the gap by
// attempting a pay run, which is the one operation an operator most wants to be
// sure about before pressing it.
// ---------------------------------------------------------------------------

// ListSalaryComponents returns one employee's salary structure, oldest window
// first and then by component code.
//
// It returns EVERY window and not just the one in force today, because an
// effective-dated master is only checkable as a sequence: an operator looking at
// "Basic 50,000" cannot tell a standing figure from one that was superseded last
// month, and the pay run reads the window in force on the FIRST of the wage
// month — which is a question about the past for every back-dated run.
func (s *Service) ListSalaryComponents(ctx context.Context, employeeID uuid.UUID) ([]SalaryComponent, error) {
	var out []SalaryComponent
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT id, employee_id, effective_from::text,
			        COALESCE(effective_to::text, ''), component_code, component_type, amount
			   FROM employee_salary_structure
			  WHERE employee_id = $1
			  ORDER BY effective_from, component_code`, employeeID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c SalaryComponent
			var typ string
			if err := rows.Scan(&c.ID, &c.EmployeeID, &c.EffectiveFrom, &c.EffectiveTo,
				&c.Code, &typ, &c.Amount); err != nil {
				return err
			}
			c.Type = payroll.ComponentType(typ)
			out = append(out, c)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("payrolldb: list salary components for employee %s: %w", employeeID, err)
	}
	return out, nil
}

// ListStatutoryElections returns one employee's elections, oldest window first.
//
// Every window, for the reason ListSalaryComponents returns every window: the
// election in force is a function of the wage month, and a reader that showed
// only today's would misdescribe every back-dated run.
func (s *Service) ListStatutoryElections(ctx context.Context, employeeID uuid.UUID) ([]StatutoryElection, error) {
	var out []StatutoryElection
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT id, employee_id, effective_from::text,
			        COALESCE(effective_to::text, ''), pf_base,
			        COALESCE(pf_employee_voluntary_base, 0),
			        pf_employer_matches_voluntary, eps_member, esi_applicable,
			        COALESCE(tax_regime, '')
			   FROM employee_statutory_election
			  WHERE employee_id = $1
			  ORDER BY effective_from`, employeeID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var e StatutoryElection
			var base, regime string
			if err := rows.Scan(&e.ID, &e.EmployeeID, &e.EffectiveFrom, &e.EffectiveTo,
				&base, &e.PFVoluntaryBase, &e.EmployerMatchVoluntary,
				&e.EPSMember, &e.ESIApplicable, &regime); err != nil {
				return err
			}
			// Passed through as STORED and never defaulted here. RegimeOrDefault
			// resolves an empty election to the s.202 default on the way IN; a
			// reader that applied it again would make a company that never chose
			// a regime indistinguishable from one that expressly elected NEW.
			e.PFBase = payroll.PFBase(base)
			e.Regime = payroll.Regime(regime)
			out = append(out, e)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("payrolldb: list statutory elections for employee %s: %w", employeeID, err)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// NULL helpers. An empty string is not the same as SQL NULL, and conflating
// them here would store '' in a PAN column that every reader then has to guard.
// ---------------------------------------------------------------------------

func nullText(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullDate(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullUUID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}
