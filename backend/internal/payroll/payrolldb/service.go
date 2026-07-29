// Package payrolldb is the DB-FACING half of payroll: it reads the employee
// master and its effective-dated salary structure, resolves the statutory
// rulesets by date, persists what was computed, and posts the pay run through
// internal/ledger.
//
// ===========================================================================
// THE SEAM: THIS PACKAGE DEFAULTS, THE ENGINE DEFAULTS NOTHING
// ===========================================================================
// internal/payroll is a PURE LEAF — its archtest entry is the empty map, it
// imports errors/fmt/sort/time/decimal and nothing else, and it can therefore
// never read a rate from a table, log a PAN or accumulate a year-to-date
// figure. Everything that requires knowing what is IN the database happens
// here:
//
//   - DEFAULTING. An employee with no recorded tax regime is on the NEW regime
//     (s.202 of the Income-tax Act 2025). An empty ruleset version is resolved
//     from the wage month. The engine applies NEITHER of those, on purpose: a
//     defaulting engine cannot tell "the employee elected the new regime" from
//     "nobody asked", and the two are different facts with the same arithmetic
//     until the day the old regime is the right answer. This is the same split
//     internal/ledger/tds.go already uses against internal/compliance.
//
//   - ACCUMULATION. Salary tax is annual (s.392), so the engine takes
//     year-to-date state AS INPUT FIELDS and never accumulates. This package
//     gathers it from the payslips already posted this tax year.
//
//   - SENTINEL WRAPPING. The engine's ErrNoRuleSet / ErrUnverifiedRate /
//     ErrUnknownRegime say what is wrong; this package adds which company,
//     which employee and which month it happened for.
//
// ===========================================================================
// WHY A TxRunner AND NOT A POOL
// ===========================================================================
// Service takes the NARROWEST CAPABILITY it needs — "run a unit of work inside
// a tenant-scoped transaction" — exactly as internal/compliance/gstr and
// internal/ledger do. Handing it a *pgxpool.Pool would let it open an
// unscoped connection, and an unscoped connection is one where
// app_current_company_id() is NULL and every RLS policy fails open into
// returning nothing (or, on a write path, into writing rows no tenant owns).
// Depending on the interface makes that unreachable rather than merely
// discouraged.
//
// EVERY read in this package goes through RunInTx for the same reason: RLS is
// what confines a payroll query to the caller's company, and RLS is keyed off
// the GUC that RunInTx sets.
//
// ===========================================================================
// PII: WHAT REACHES THE ENGINE, AND WHAT NEVER DOES
// ===========================================================================
// PAN, UAN, ESIC IP number, employee name and bank details are read here,
// validated here (employee.go) and written to the statutory files by
// payrollfile — they NEVER cross into internal/payroll. The engine takes
// `PANAvailable bool`, copying internal/compliance/tds.go's s.206AA pattern
// exactly, so the leaf can never print, log or hash a real identifier.
//
// Nothing in payroll, payrolldb or payrollfile imports "log" or "log/slog" or
// writes to stdout/stderr; TestPayrollPackagesDoNotLog is the AST scan that
// keeps it that way.
package payrolldb

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/payroll"
	"github.com/khata/cored/internal/platform/db"
)

// TxRunner is the minimal database capability this package needs: run a unit of
// work inside a tenant-scoped transaction. *db.Pool satisfies it. See the
// package doc for why this is an interface and not a pool.
type TxRunner interface {
	RunInTx(ctx context.Context, fn db.TxFunc) error
}

// Service is the payroll application service: the employee master, the pay run
// and the read models behind the register and the payslip.
type Service struct {
	runner TxRunner
	ledger *ledger.Service
}

// NewService constructs a payroll Service over a transaction runner and the
// ledger service it posts through.
//
// It takes the LEDGER SERVICE and not a second TxRunner because a pay run must
// go through ledger.post() — the single posting path — and not around it. Every
// guarantee the books rest on lives inside that function: the hash chain, the
// version chain, the freeze-on-external-reference gate, the numbering series
// and the balance update. A payroll module that wrote postings itself would
// have all of them as advisory.
func NewService(runner TxRunner, led *ledger.Service) *Service {
	return &Service{runner: runner, ledger: led}
}

// ---------------------------------------------------------------------------
// Ruleset resolution + defaulting. The engine resolves; this decides WHEN.
// ---------------------------------------------------------------------------

// RuleSets is the pair of effective-dated rulesets a wage month computes under,
// together with the version strings stamped on the pay_run row.
//
// The versions are persisted and the RATES are not, and that asymmetry is the
// whole design: rates are LAW, identical in every tenant, and live in Go where
// they change in a diff with a reviewer and are re-derived independently by the
// corpus oracle. A company_id-scoped rate table would let two companies compute
// different statutory tax, which is a wrong-number bug with no error path.
// What the database needs is only enough to make a RE-RUN verifiable: which
// window resolved when the month was first computed.
type RuleSets struct {
	SocialSecurity payroll.SocialSecurityRuleSet
	IncomeTax      payroll.IncomeTaxRuleSet
}

// SSVersion returns the social-security ruleset version string.
func (r RuleSets) SSVersion() string { return r.SocialSecurity.Version }

// ITVersion returns the income-tax ruleset version string.
func (r RuleSets) ITVersion() string { return r.IncomeTax.Version }

// ResolveRuleSets resolves both rulesets for a wage month.
//
// Social security resolves on the LAST DAY of the wage month (a scheme change
// that lands on a calendar date mid-year governs the wages payable for that
// whole month); income tax resolves on the TAX YEAR, because its base is annual
// and its ruleset must be constant across the year even when a social-security
// scheme change lands inside it.
//
// An uncovered period is an ERROR and never a fallback to the newest or oldest
// window. That refusal is the engine's, and this wrapper only says which month
// it was asked about — a pay run computed under the wrong year's rates is a
// plausible, wrong, un-auditable number that nobody would ever think to query.
func ResolveRuleSets(m payroll.WageMonth) (RuleSets, error) {
	ss, err := payroll.SocialSecurityForMonth(m)
	if err != nil {
		return RuleSets{}, fmt.Errorf("payrolldb: resolve social-security ruleset for %s: %w", m, err)
	}
	it, err := payroll.IncomeTaxFor(m.TaxYear())
	if err != nil {
		return RuleSets{}, fmt.Errorf("payrolldb: resolve income-tax ruleset for %s (tax year %s): %w", m, m.TaxYear(), err)
	}
	return RuleSets{SocialSecurity: ss, IncomeTax: it}, nil
}

// RegimeOrDefault resolves a stored regime string, applying the s.202 default
// when the employee has made no election.
//
// THE DEFAULTING LIVES HERE AND NOT IN payroll.RegimeFor, which errors on an
// empty string on purpose. RegimeFor is a VOCABULARY CHECK: it exists so that a
// value nobody implemented yet is refused rather than quietly treated as the
// new regime. If it also defaulted, a typo ("NEWW") and a genuine non-election
// would be indistinguishable, and the typo would compute under the wrong slabs
// forever.
func RegimeOrDefault(s string) (payroll.Regime, error) {
	if s == "" {
		return payroll.DefaultRegime, nil
	}
	r, err := payroll.RegimeFor(s)
	if err != nil {
		return "", fmt.Errorf("payrolldb: employee tax regime: %w", err)
	}
	return r, nil
}

// PFBaseOrDefault resolves a stored PF base string.
//
// Unlike the regime there is NO DEFAULT: CEILING and ACTUAL produce materially
// different contributions for anyone paid above the wage ceiling, which is most
// salaried staff, and guessing either way is somebody's money. An empty value
// is an error naming both options.
func PFBaseOrDefault(s string) (payroll.PFBase, error) {
	if s == "" {
		return "", fmt.Errorf("payrolldb: PF contribution base is not set for this employee; it must be CEILING or ACTUAL (they differ for anyone paid above the wage ceiling, so there is no safe default): %w",
			payroll.ErrUnknownPFBase)
	}
	b, err := payroll.PFBaseFor(s)
	if err != nil {
		return "", fmt.Errorf("payrolldb: employee PF base: %w", err)
	}
	return b, nil
}

// ---------------------------------------------------------------------------
// The control ledgers. Resolved BY NAME, seeded by migration 0034.
// ---------------------------------------------------------------------------

// The seven control ledgers a pay run posts to. They are seeded by name in
// every tenant (migration 0034) so this package can resolve them with a name
// lookup rather than importing internal/masters or making the caller pass seven
// ledger ids on every run — which would let one tenant post its PF liability to
// its Sales ledger.
const (
	// LedgerSalariesAndWages carries GROSS earnings (Indirect Expenses).
	LedgerSalariesAndWages = "Salaries & Wages"
	// LedgerEmployerPF carries the employer's EPF + EPS cost (Indirect Expenses).
	LedgerEmployerPF = "Employer PF Contribution"
	// LedgerEmployerESI carries the employer's ESI share (Indirect Expenses).
	LedgerEmployerESI = "Employer ESI Contribution"
	// LedgerSalaryPayable is the AGGREGATE net-pay liability (Current
	// Liabilities). One control ledger by default: an employee-named ledger
	// would put every individual salary into the Day Book, which the seeded Data
	// Entry level can read in every tenant.
	LedgerSalaryPayable = "Salary Payable"
	// LedgerTDSOnSalaryPayable is the s.392 deduction (Duties & Taxes).
	LedgerTDSOnSalaryPayable = "TDS on Salary Payable"
	// LedgerPFPayable is the employee + employer provident fund liability.
	LedgerPFPayable = "PF Payable"
	// LedgerESIPayable is the employee + employer ESI liability.
	LedgerESIPayable = "ESI Payable"
)

// ErrControlLedgerMissing is returned when a company's chart of accounts does
// not carry one of the seven control ledgers migration 0034 seeds.
//
// It is a REFUSAL and not a create-on-demand, deliberately. Creating the ledger
// here would place it under whatever group happened to resolve, and a payable
// filed under an expense group is a balance sheet that is wrong by the whole
// payroll liability. The migration is the one place that decides where these
// ledgers live.
var ErrControlLedgerMissing = errors.New("payrolldb: payroll control ledger is missing from this company's chart of accounts")

// controlLedgerIDs resolves all seven control ledgers in ONE query inside the
// caller's transaction, so the pay run cannot half-resolve and post an
// unbalanced entry naming five of seven ledgers.
func controlLedgerIDs(ctx context.Context, tx pgx.Tx) (map[string]uuid.UUID, error) {
	names := []string{
		LedgerSalariesAndWages, LedgerEmployerPF, LedgerEmployerESI,
		LedgerSalaryPayable, LedgerTDSOnSalaryPayable,
		LedgerPFPayable, LedgerESIPayable,
	}
	rows, err := tx.Query(ctx,
		`SELECT name, id FROM ledger WHERE name = ANY($1)`, names)
	if err != nil {
		return nil, fmt.Errorf("payrolldb: resolve control ledgers: %w", err)
	}
	defer rows.Close()
	out := make(map[string]uuid.UUID, len(names))
	for rows.Next() {
		var n string
		var id uuid.UUID
		if err := rows.Scan(&n, &id); err != nil {
			return nil, fmt.Errorf("payrolldb: scan control ledger: %w", err)
		}
		out[n] = id
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("payrolldb: read control ledgers: %w", err)
	}
	for _, n := range names {
		if _, ok := out[n]; !ok {
			return nil, fmt.Errorf("%w: %q (migration 0034 seeds it; a company whose chart predates that migration and was not backfilled will fail here)", ErrControlLedgerMissing, n)
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Money helpers.
// ---------------------------------------------------------------------------

// money is the storage scale of every NUMERIC(19,4) column in the payroll
// schema. It is used ONLY to render a decimal for an error message or to sum
// persisted figures — never to round a statutory amount. All four statutory
// roundings live in internal/payroll/rounding.go and nowhere else, and
// TestRoundingHelpersAreTheOnlyRounders fails the build if a second rounder
// appears in the engine.
const money = 4

// sumOf adds a slice of decimals left to right. decimal.Decimal is exact, so
// the order is irrelevant to the result; it is written as a helper only so the
// leg derivation below reads as arithmetic rather than as a loop.
func sumOf(ds ...decimal.Decimal) decimal.Decimal {
	total := decimal.Zero
	for _, d := range ds {
		total = total.Add(d)
	}
	return total
}
