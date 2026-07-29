package payrolldb_test

// employee_test.go — the master, its identifier formats, and the Aadhaar
// negative control.

import (
	"errors"
	"strings"
	"testing"

	"github.com/khata/cored/internal/payroll"
	"github.com/khata/cored/internal/payroll/payrolldb"
)

// TestEmployeeRejectsAadhaarShapedInput proves a genuinely Verhoeff-valid
// twelve-digit number is refused from every free-text employee field.
//
// WHY THIS TEST IS THE POINT OF THE VERHOEFF CODE. Migration 0034 stores no
// Aadhaar and never will — no statutory output in scope needs one, and there is
// no at-rest protection in this plane to store one behind. But the absence of a
// column is not the absence of the data: the realistic leak is somebody typing
// an Aadhaar into `designation` because there was nowhere else to put it, after
// which it sits in plaintext, is read by the register and is exported to CSV.
//
// The NEGATIVE CONTROLS in the second half are what make the refusal narrow
// enough to be usable. A length-only rule would reject an ordinary twelve-digit
// staff number, and a rule people trip over for no reason is a rule that gets
// removed.
func TestEmployeeRejectsAadhaarShapedInput(t *testing.T) {
	pool, ctx, _ := setup(t)
	svc := newService(pool)

	// 234123412346 — twelve digits, Verhoeff-valid. Verified independently by
	// TestVerhoeffAgainstKnownVectors below, which checks that mutating any one
	// digit and transposing any adjacent pair both make it invalid (the two
	// error classes Verhoeff was designed to catch).
	const aadhaarShaped = "234123412346"

	for _, tc := range []struct {
		field string
		emp   payrolldb.Employee
	}{
		{"designation", payrolldb.Employee{Code: "E-A-1", Name: "Someone", DOJ: "2026-01-01", Designation: aadhaarShaped}},
		{"code", payrolldb.Employee{Code: aadhaarShaped, Name: "Someone", DOJ: "2026-01-01"}},
		{"name", payrolldb.Employee{Code: "E-A-2", Name: aadhaarShaped, DOJ: "2026-01-01"}},
	} {
		_, err := svc.CreateEmployee(ctx, tc.emp)
		if err == nil {
			t.Errorf("an Aadhaar-shaped value was accepted in %s; it would sit in plaintext in a table with no special handling, "+
				"be read by the register and be exported to CSV", tc.field)
			continue
		}
		if !errors.Is(err, payrolldb.ErrAadhaarShapedValue) {
			t.Errorf("%s: wrong error for an Aadhaar-shaped value: %v", tc.field, err)
		}
		if !strings.Contains(err.Error(), tc.field) {
			t.Errorf("%s: the refusal does not name the field: %v", tc.field, err)
		}
	}

	// NEGATIVE CONTROLS. None of these may be refused.
	for _, ok := range []struct{ what, value string }{
		{"an ordinary twelve-digit staff number (Verhoeff-invalid)", "234123412345"},
		{"an eleven-digit number", "23412341234"},
		{"a thirteen-digit number", "2341234123456"},
		{"a designation with digits in it", "Grade 12 Analyst"},
	} {
		if payrolldb.IsAadhaarShaped(ok.value) {
			t.Errorf("%s (%q) was treated as an Aadhaar; a rule people trip over for no reason is a rule that gets removed",
				ok.what, ok.value)
		}
	}
}

// TestVerhoeffAgainstKnownVectors checks the three transcription-sensitive
// tables against vectors whose validity is known independently of this code.
//
// The mutants are the evidence, not the base vector: Verhoeff exists to catch
// SINGLE-DIGIT errors and ADJACENT TRANSPOSITIONS, so a correct implementation
// must reject every one of them. A table with a typo in it would accept some.
func TestVerhoeffAgainstKnownVectors(t *testing.T) {
	const valid = "234123412346"
	if !payrolldb.IsAadhaarShaped(valid) {
		t.Fatalf("%s should be Verhoeff-valid; the tables are mis-transcribed", valid)
	}

	// Every single-digit mutation must become invalid.
	for i := 0; i < len(valid); i++ {
		for d := byte('0'); d <= '9'; d++ {
			if d == valid[i] {
				continue
			}
			m := []byte(valid)
			m[i] = d
			if payrolldb.IsAadhaarShaped(string(m)) {
				t.Errorf("single-digit mutant %s (position %d) is accepted; Verhoeff catches every single-digit error, so a table is wrong", string(m), i)
			}
		}
	}
	// Every adjacent transposition of DIFFERING digits must become invalid.
	for i := 0; i+1 < len(valid); i++ {
		if valid[i] == valid[i+1] {
			continue
		}
		m := []byte(valid)
		m[i], m[i+1] = m[i+1], m[i]
		if payrolldb.IsAadhaarShaped(string(m)) {
			t.Errorf("adjacent transposition %s (positions %d,%d) is accepted; Verhoeff catches every adjacent transposition", string(m), i, i+1)
		}
	}
}

// TestEmployeeRejectsMalformedPANAndIFSC proves the two identifiers whose
// malformation costs real money are checked, and that a blank one is still
// allowed.
//
// A malformed PAN is a Form 138 deductee record the department rejects, which
// puts the deductor in default. A malformed IFSC is a salary paid to nobody.
// But an employee below the PF and ESI thresholds genuinely has no UAN and no
// IP number, and refusing to record them at all would be worse than recording
// them without one — so ABSENT and MALFORMED are different answers.
func TestEmployeeRejectsMalformedPANAndIFSC(t *testing.T) {
	pool, ctx, _ := setup(t)
	svc := newService(pool)

	base := func() payrolldb.Employee {
		return payrolldb.Employee{Code: "E-FMT", Name: "Format", DOJ: "2026-01-01", IsActive: true}
	}
	bad := []struct {
		what string
		mut  func(*payrolldb.Employee)
	}{
		{"PAN with a digit where a letter belongs", func(e *payrolldb.Employee) { e.PAN = "ABCP12345" }},
		{"PAN that is lowercase", func(e *payrolldb.Employee) { e.PAN = "abcpd1234e" }},
		{"PAN one character too long", func(e *payrolldb.Employee) { e.PAN = "ABCPD1234EF" }},
		// The fifth character of an IFSC is RESERVED and is always '0'. This is
		// the one people get wrong, and an IFSC with a letter there is not a
		// bank branch at all.
		{"IFSC without the reserved zero", func(e *payrolldb.Employee) { e.BankIFSC = "HDFCA001234" }},
		{"IFSC that is too short", func(e *payrolldb.Employee) { e.BankIFSC = "HDFC000123" }},
		{"UAN that is not twelve digits", func(e *payrolldb.Employee) { e.UAN = "12345" }},
		{"ESIC IP number that is not ten digits", func(e *payrolldb.Employee) { e.ESICIPNo = "12345" }},
		{"a FULL bank account number in the last4 column", func(e *payrolldb.Employee) { e.BankAccountLast4 = "50100123456789" }},
	}
	for _, tc := range bad {
		e := base()
		tc.mut(&e)
		if _, err := svc.CreateEmployee(ctx, e); err == nil {
			t.Errorf("%s was accepted", tc.what)
		} else if !errors.Is(err, payrolldb.ErrInvalidEmployeeField) {
			t.Errorf("%s: wrong error: %v", tc.what, err)
		}
	}

	// ABSENT is not MALFORMED: an employee below both thresholds records
	// neither a UAN nor an IP number, and must still be creatable.
	if _, err := svc.CreateEmployee(ctx, payrolldb.Employee{
		Code: "E-FMT-OK", Name: "No Identifiers", DOJ: "2026-01-01", IsActive: true,
	}); err != nil {
		t.Errorf("an employee with no PAN, UAN, IP number or bank details was refused: %v", err)
	}
}

// TestSalaryComponentTypeMustBeInTheClosedVocabulary proves a component type
// nobody implemented is refused rather than silently excluded from wages.
//
// s.2(88) of the Code on Social Security lists the EXCLUSIONS from "wages"
// exhaustively, and everything not on that list IS wages. A component that fell
// through to "excluded" would under-contribute PF and ESI for the whole of that
// employee's service, with no error anywhere and no report that shows it.
func TestSalaryComponentTypeMustBeInTheClosedVocabulary(t *testing.T) {
	pool, ctx, _ := setup(t)
	svc := newService(pool)

	emp := createEmployee(t, svc, ctx, "E-CT-1", "Component Type")
	_, err := svc.SetSalaryComponent(ctx, payrolldb.SalaryComponent{
		EmployeeID: emp, EffectiveFrom: "2026-04-01",
		Code: "SITE_ALLOWANCE", Type: payroll.ComponentType("SITE_ALLOWANCE"),
		Amount: dec("5000"),
	})
	if err == nil {
		t.Fatal("a component type outside the closed vocabulary was accepted; it would be classified by fall-through and could under-contribute PF and ESI for the whole of that employee's service")
	}
	if !errors.Is(err, payroll.ErrUnknownComponentType) {
		t.Fatalf("wrong error for an unknown component type: %v", err)
	}
}

// TestOverlappingEffectiveWindowsAreRefused proves the EXCLUDE constraints in
// migration 0034 bite for both effective-dated children.
//
// Two overlapping rows are not an error anywhere in the READ path — the reader
// would pick one by whatever ORDER BY it happened to use — so the same month
// would compute differently depending on insertion order. The refusal has to be
// in the schema.
func TestOverlappingEffectiveWindowsAreRefused(t *testing.T) {
	pool, ctx, _ := setup(t)
	svc := newService(pool)

	emp := createEmployee(t, svc, ctx, "E-OV-1", "Overlapping")

	if _, err := svc.SetSalaryComponent(ctx, payrolldb.SalaryComponent{
		EmployeeID: emp, EffectiveFrom: "2026-04-01", EffectiveTo: "2026-10-01",
		Code: "BASIC", Type: payroll.ComponentBasic, Amount: dec("30000"),
	}); err != nil {
		t.Fatalf("first salary component: %v", err)
	}
	if _, err := svc.SetSalaryComponent(ctx, payrolldb.SalaryComponent{
		EmployeeID: emp, EffectiveFrom: "2026-07-01",
		Code: "BASIC", Type: payroll.ComponentBasic, Amount: dec("35000"),
	}); err == nil {
		t.Error("two overlapping BASIC windows were accepted; a pay run for July would have two answers to what the basic is, decided by ORDER BY")
	}

	// The HALF-OPEN boundary is NOT an overlap: a window ending 2026-10-01 and
	// one starting 2026-10-01 are adjacent, and that is the ordinary shape of a
	// salary revision. If this were refused, no revision could ever be recorded
	// without leaving a one-day hole.
	if _, err := svc.SetSalaryComponent(ctx, payrolldb.SalaryComponent{
		EmployeeID: emp, EffectiveFrom: "2026-10-01",
		Code: "BASIC", Type: payroll.ComponentBasic, Amount: dec("35000"),
	}); err != nil {
		t.Errorf("an ADJACENT window (from = the previous window's exclusive to) was refused: %v — half-open [from,to) means these do not overlap", err)
	}

	if _, err := svc.SetStatutoryElection(ctx, payrolldb.StatutoryElection{
		EmployeeID: emp, EffectiveFrom: "2026-04-01",
		PFBase: payroll.PFBaseCeiling, EPSMember: true, ESIApplicable: true, Regime: payroll.RegimeNew,
	}); err != nil {
		t.Fatalf("first election: %v", err)
	}
	if _, err := svc.SetStatutoryElection(ctx, payrolldb.StatutoryElection{
		EmployeeID: emp, EffectiveFrom: "2026-06-01",
		PFBase: payroll.PFBaseActual, EPSMember: true, ESIApplicable: true, Regime: payroll.RegimeOld,
	}); err == nil {
		t.Error("two overlapping elections were accepted; the same month would have two answers to which PF base and which tax regime apply")
	}
}

// TestPFBaseHasNoDefaultAndRegimeDoes pins the deliberate asymmetry between the
// two vocabulary selectors.
//
// The regime HAS a default because s.202 supplies one: an employee who elects
// nothing is on the new regime, as a matter of law. The PF base does NOT,
// because CEILING and ACTUAL produce materially different contributions for
// anyone paid above the wage ceiling — which is most salaried staff — and there
// is no statute that picks one. Guessing would be somebody's money.
func TestPFBaseHasNoDefaultAndRegimeDoes(t *testing.T) {
	if r, err := payrolldb.RegimeOrDefault(""); err != nil || r != payroll.DefaultRegime {
		t.Errorf("RegimeOrDefault(\"\") = %q, %v; want the s.202 default (%q) and no error", r, err, payroll.DefaultRegime)
	}
	if _, err := payrolldb.RegimeOrDefault("NEWW"); err == nil {
		t.Error("a typo'd regime was accepted; a vocabulary check that also defaults cannot tell a typo from a non-election, and the typo would compute under the wrong slabs forever")
	}
	if _, err := payrolldb.PFBaseOrDefault(""); err == nil {
		t.Error("an unset PF base was accepted; CEILING and ACTUAL differ for anyone paid above the wage ceiling and there is no safe default")
	}
	if b, err := payrolldb.PFBaseOrDefault("ACTUAL"); err != nil || b != payroll.PFBaseActual {
		t.Errorf("PFBaseOrDefault(\"ACTUAL\") = %q, %v", b, err)
	}
}

// TestRuleSetsRefuseAnUncoveredMonth proves a wage month no window covers is an
// ERROR rather than a silent fallback.
//
// A June 2019 pay run computed under July 2026 rates is a plausible, wrong,
// un-auditable number that nobody would ever think to query. This is
// inventory.ValuerFor's reasoning applied to time, and it is the bug the
// compliance path's DefaultTDSRulesetVersion still has.
func TestRuleSetsRefuseAnUncoveredMonth(t *testing.T) {
	if _, err := payrolldb.ResolveRuleSets(payroll.WageMonth{Year: 2015, Month: 6}); err == nil {
		t.Fatal("a wage month before every shipped ruleset resolved successfully; it must have silently fallen back to a window that did not exist then")
	} else if !errors.Is(err, payroll.ErrNoRuleSet) {
		t.Fatalf("wrong error for an uncovered wage month: %v", err)
	}
	if _, err := payrolldb.ResolveRuleSets(payroll.WageMonth{Year: 2099, Month: 6}); err == nil {
		t.Fatal("a wage month after every shipped income-tax ruleset resolved successfully; the slabs are re-enacted by a Finance Act every year and running a future year under this year's is a guess about an Act that does not exist")
	}
}
