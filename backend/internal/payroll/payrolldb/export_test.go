package payrolldb_test

// export_test.go — the GOLDEN-FILE tests for the payrolldb -> payrollfile row
// mappers, and the refusals that keep a wrong filing from being emitted.
//
// ===========================================================================
// WHY THESE ARE GOLDEN FILES AND NOT FIELD-BY-FIELD ASSERTIONS
// ===========================================================================
// The failure these mappers exist to prevent is a file the portal REJECTS, or
// worse accepts with a wrong number in it. A field-by-field assertion written
// from the mapper's own source cannot catch either: it agrees with whatever the
// mapper does. A whole expected FILE, written out here as the bytes an operator
// would upload, catches a column swapped for its neighbour, a delimiter changed,
// a rounding introduced, and a wage collapsed into another wage.
//
// Every figure in the expectations below is HAND-DERIVED from the statute and
// the shipped rate table, and the derivation is reproduced in the fixture
// comment so a reader can check it without running anything. Nothing here was
// produced by running the code and pasting the output.
//
// ===========================================================================
// NOT VALIDATOR-VERIFIED
// ===========================================================================
// These tests prove the mapper produces the DOCUMENTED layout. They do NOT
// prove the layout is right. The NSDL/Protean FVU, the EPFO Unified Portal and
// the ESIC employer portal cannot run in this environment and NONE of them has
// seen a byte of this output. See payrollfile's package doc.

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/payroll"
	"github.com/khata/cored/internal/payroll/payrolldb"
	"github.com/khata/cored/internal/payroll/payrollfile"
)

// exportMonth is the wage month every test in this file files for.
var exportMonth = payroll.WageMonth{Year: 2026, Month: time.July}

// ---------------------------------------------------------------------------
// THE FIXTURE, AND THE HAND DERIVATION OF EVERY FIGURE THE FILES CARRY.
//
// July 2026 -> social-security ruleset "ss-2026-07" (EPF 12%, EPS 8.33%, all
// three wage ceilings 15,000; ESI 0.75/3.25, coverage ceiling 21,000) and
// income-tax ruleset "it-ty-2026-27". The month has 31 days, so the resolved
// paid days are 31 and the loss-of-pay days are 0.
//
// EMPLOYEE A — code E-EXP-A, a single BASIC of 2,00,000 from 1 Apr 2026.
// (The identical structure payrun_test.go derives at lines 66-88.)
//
//	s.2(88): BASIC is not on the exclusion list   STATUTORY WAGE  2,00,000
//	EPF base CEILING: min(2,00,000, 15,000) = 15,000, and EPS and EDLI wages
//	  are the same 15,000 ceiling
//	  employee EPF  12% x 15,000                                     1,800
//	  EPS           8.33% x 15,000 = 1,249.50 -> 1,250
//	  employer EPF  1,800 - 1,250                                      550   <- STORED NET OF EPS
//	  employer TOTAL share (what the ECR's field 9 is derived from)   1,800
//	ESI: coverage latched on 1 Apr 2026 at 2,00,000 > 21,000 -> NOT COVERED.
//	  Both ESI shares are zero, so A is ABSENT from the ESI return entirely.
//	s.392: no April-June payslips in a throwaway company, so the annual
//	  estimate is this month plus the eight after it:
//	    2,00,000 x 9 = 18,00,000, less the s.19 standard deduction 75,000
//	    = 17,25,000; slabs 5/10/15/20% = 1,45,000; cess 4% = 5,800
//	    annual tax 1,50,800, spread over 9 months = 16,755.55... -> 16,756
//	  TDS = 16,756, which is what the Form 138 deductee record carries.
//
// EMPLOYEE B — code E-EXP-B, a single BASIC of 18,000 from 1 Apr 2026.
//
//	STATUTORY WAGE 18,000; EPF/EPS/EDLI wages min(18,000, 15,000) = 15,000
//	  employee EPF 1,800; EPS 1,250; employer EPF 550 (net); total share 1,800
//	ESI: latched at 18,000 <= 21,000 -> COVERED. The ESI-reckonable wage is
//	  18,000 (no contribution ceiling; only coverage has one):
//	    employee 0.75% x 18,000 = 135 ; employer 3.25% x 18,000 = 585
//	s.392: 18,000 x 9 = 1,62,000 less 75,000 = 87,000, far below the s.156
//	  rebate limit -> TDS = 0. B therefore has NO Form 138 deductee record:
//	  Annexure I is the statement of tax DEDUCTED.
//
// THE THREE FILES THIS PRODUCES
//	ECR          both employees (both are provident-fund members)
//	ESI return   B only         (A is not covered)
//	Form 138     A only         (B had no deduction)
// ---------------------------------------------------------------------------

const (
	exportUANA = "100200300401"
	exportUANB = "100200300402"
	exportIPA  = "1234567891"
	exportIPB  = "1234567892"
	exportPANA = "ABCPD1234E"
	exportPANB = "ABCPD1234F"
)

// exportFixture creates the two employees, lays down their structures and
// elections, and POSTS the July run. It returns their ids.
func exportFixture(t *testing.T, svc *payrolldb.Service, ctx context.Context) (a, b uuid.UUID) {
	t.Helper()
	a = exportEmployee(t, svc, ctx, "E-EXP-A", "Above ESI Ceiling", exportPANA, exportUANA, exportIPA, "")
	b = exportEmployee(t, svc, ctx, "E-EXP-B", "Within ESI Ceiling", exportPANB, exportUANB, exportIPB, "")
	setStructureAndElectionAt(t, svc, ctx, a, "2026-04-01", wageAboveESICeiling)
	setStructureAndElectionAt(t, svc, ctx, b, "2026-04-01", wageWithinESICeiling)
	res, err := svc.ComputeAndRunPayRun(ctx, payrolldb.ComputePayRunInput{
		Month:     exportMonth,
		CreatedBy: "payroll-export-test",
		Narration: "Payroll for " + exportMonth.String(),
	})
	if err != nil {
		t.Fatalf("compute and run pay run: %v", err)
	}
	if res.Status != payrolldb.StatusPosted {
		t.Fatalf("pay run status = %q, want POSTED — a statutory file may only be emitted from a posted run", res.Status)
	}
	return a, b
}

// exportEmployee creates one employee with DISTINCT statutory identifiers.
//
// Distinct on purpose: the shared helpers in this package give every employee
// the same UAN and the same IP number, which is fine for a posting test and
// useless here — a mapper that emitted row 1's identifier on every line would
// pass against identical fixtures and file everybody's wages under one member.
func exportEmployee(t *testing.T, svc *payrolldb.Service, ctx context.Context,
	code, name, pan, uan, ip, dol string) uuid.UUID {
	t.Helper()
	id, err := svc.CreateEmployee(ctx, payrolldb.Employee{
		Code: code, Name: name, DOJ: "2025-06-01", DOL: dol,
		Designation: "Analyst", PAN: pan, UAN: uan, ESICIPNo: ip,
		BankIFSC: "HDFC0001234", BankAccountLast4: "4321", IsActive: true,
	})
	if err != nil {
		t.Fatalf("create employee %s: %v", code, err)
	}
	return id
}

// ---------------------------------------------------------------------------
// PF ECR.
// ---------------------------------------------------------------------------

// TestECRGoldenFile pins the whole emitted ECR, byte for byte.
//
// The load-bearing column is FIELD 9, "EPF & EPS Diff Remitted": 550. The
// payslip stores the employer's EPF share ALREADY NET of EPS (550), while
// payrollfile wants the employer's TOTAL share and subtracts EPS from it. A
// mapper that passed the net figure straight through makes EPS (1,250) exceed
// the employer share (550) and the file is refused outright; a mapper that
// double-counted would report 1,800 in field 9 and over-state every
// establishment's provident-fund remittance by the whole pension share.
func TestECRGoldenFile(t *testing.T) {
	pool, ctx, _ := setup(t)
	svc := newService(pool)
	exportFixture(t, svc, ctx)

	var buf bytes.Buffer
	if err := svc.WriteECR(ctx, &buf, exportMonth); err != nil {
		t.Fatalf("write ECR: %v", err)
	}

	// Eleven `#~#`-delimited fields per member, no header, no trailer, ordered
	// by employee code. Every figure is derived in the fixture comment above.
	want := strings.Join([]string{
		exportUANA + "#~#Above ESI Ceiling#~#200000#~#15000#~#15000#~#15000#~#1800#~#1250#~#550#~#0#~#0",
		exportUANB + "#~#Within ESI Ceiling#~#18000#~#15000#~#15000#~#15000#~#1800#~#1250#~#550#~#0#~#0",
	}, "\n") + "\n"

	if got := buf.String(); got != want {
		t.Fatalf("the emitted PF ECR is not the file the EPFO Unified Portal would be given.\n got:\n%s\nwant:\n%s\n"+
			"Every line is one member's wages and contributions for July 2026; a wrong column here is a "+
			"mis-credited provident fund the member carries for the rest of their service.", got, want)
	}
}

// TestECRRefusesAMemberWithNoUAN proves the mandatory identifier is a REFUSAL
// and never a blank field.
//
// A blank UAN is not a partial filing: under ECR 2.0 the portal either rejects
// the upload or credits the contribution to nobody, and the employer discovers
// it after the challan window has closed.
func TestECRRefusesAMemberWithNoUAN(t *testing.T) {
	pool, ctx, _ := setup(t)
	svc := newService(pool)
	noUAN := exportEmployee(t, svc, ctx, "E-EXP-NOUAN", "No UAN Recorded", exportPANA, "", exportIPA, "")
	setStructureAndElectionAt(t, svc, ctx, noUAN, "2026-04-01", wageWithinESICeiling)
	if _, err := svc.ComputeAndRunPayRun(ctx, payrolldb.ComputePayRunInput{
		Month: exportMonth, CreatedBy: "payroll-export-test",
	}); err != nil {
		t.Fatalf("compute and run pay run: %v", err)
	}

	var buf bytes.Buffer
	err := svc.WriteECR(ctx, &buf, exportMonth)
	if !errors.Is(err, payrolldb.ErrMissingStatutoryIdentifier) {
		t.Fatalf("WriteECR for a member with no UAN returned %v, want ErrMissingStatutoryIdentifier. "+
			"A blank UAN credits somebody's provident fund to nobody.", err)
	}
	if !strings.Contains(err.Error(), "E-EXP-NOUAN") {
		t.Errorf("the refusal does not name the employee code: %v — an operator with two hundred staff cannot act on it", err)
	}
	if buf.Len() != 0 {
		t.Errorf("WriteECR wrote %d bytes while refusing; a half-written ECR looks like a successful filing", buf.Len())
	}
}

// ---------------------------------------------------------------------------
// ESI monthly contribution return.
// ---------------------------------------------------------------------------

// TestESIReturnGoldenFile pins the emitted CSV, and with it the two decisions
// the mapper makes that nothing else in the codebase makes: WHO is on the
// return (only the covered), and what the wage column carries.
func TestESIReturnGoldenFile(t *testing.T) {
	pool, ctx, _ := setup(t)
	svc := newService(pool)
	exportFixture(t, svc, ctx)

	var buf bytes.Buffer
	if err := svc.WriteESIReturn(ctx, &buf, exportMonth, payrolldb.ESIOptions{}); err != nil {
		t.Fatalf("write ESI return: %v", err)
	}

	// Employee A is ABSENT: latched above the 21,000 coverage ceiling, they are
	// not an insured person and a zero-wage row for them is a different (and
	// wrong) statement from no row at all.
	want := "IP Number,IP Name,No. of Days for which wages paid,Total Monthly Wages,Reason code for 0 wages,Last Working Day\n" +
		exportIPB + ",Within ESI Ceiling,31,18000,,\n"

	if got := buf.String(); got != want {
		t.Fatalf("the emitted ESI contribution return is not the file the ESIC template would be given.\n got:\n%q\nwant:\n%q\n"+
			"A wrong wage or a wrong day count here changes what an insured person's family can claim.", got, want)
	}
	if strings.Contains(buf.String(), exportIPA) {
		t.Errorf("employee A appears on the ESI return; they are latched above the coverage ceiling and are not an insured person")
	}
}

// TestESIReturnRefusesALeaverWithNoReason is the "never a silent blank" test
// for the one field the database cannot supply.
//
// The employee master records a DATE of leaving and not WHY. "Retired",
// "Expired" and "Employee has left service" are three different filings against
// the same date, and the ESIC column is literally called "Reason code for 0
// wages" — a leaver filed with a blank reason is a row the portal cannot accept
// and a contribution history that is wrong in the employee's disfavour.
func TestESIReturnRefusesALeaverWithNoReason(t *testing.T) {
	pool, ctx, _ := setup(t)
	svc := newService(pool)
	// Left ON 31 July 2026 — inside the wage month. The engine refuses a
	// MID-month leaving date (a part month needs proration, which is not
	// modelled), so the last day of the month is the only leaving date a run
	// can carry, and it is the case the ESI return's Last Working Day exists for.
	leaver := exportEmployee(t, svc, ctx, "E-EXP-LEFT", "Left In July", exportPANB, exportUANB, exportIPB, "2026-07-31")
	setStructureAndElectionAt(t, svc, ctx, leaver, "2026-04-01", wageWithinESICeiling)
	if _, err := svc.ComputeAndRunPayRun(ctx, payrolldb.ComputePayRunInput{
		Month: exportMonth, CreatedBy: "payroll-export-test",
	}); err != nil {
		t.Fatalf("compute and run pay run: %v", err)
	}

	var buf bytes.Buffer
	err := svc.WriteESIReturn(ctx, &buf, exportMonth, payrolldb.ESIOptions{})
	if !errors.Is(err, payrolldb.ErrLeavingReasonRequired) {
		t.Fatalf("WriteESIReturn for a July leaver with no supplied reason returned %v, want ErrLeavingReasonRequired", err)
	}
	if !strings.Contains(err.Error(), "E-EXP-LEFT") {
		t.Errorf("the refusal does not name the employee code: %v", err)
	}

	// Supplied, the SAME month files — and it carries the last working day the
	// portal needs, in the ESIC's own DD/MM/YYYY form.
	buf.Reset()
	if err := svc.WriteESIReturn(ctx, &buf, exportMonth, payrolldb.ESIOptions{
		LeavingReasons: map[string]string{"E-EXP-LEFT": payrollfile.ESIReasonLeftService},
	}); err != nil {
		t.Fatalf("write ESI return with the reason supplied: %v", err)
	}
	if !strings.Contains(buf.String(), ",Employee has left service,31/07/2026\n") {
		t.Fatalf("the leaver's row does not carry the reason and last working day:\n%q", buf.String())
	}
}

// ---------------------------------------------------------------------------
// Form 138 (ex-24Q) Annexure I.
// ---------------------------------------------------------------------------

// form138Fixture is the deductor/challan block. NONE of it is in the database —
// the company master carries no TAN and no challan — so it is the operator's to
// supply and every field is stated here rather than defaulted.
func form138Input() payrolldb.Form138Input {
	return payrolldb.Form138Input{
		TaxYear:            payroll.TaxYear{StartYear: 2026},
		Quarter:            "Q2", // July is in Q2 (Jul-Sep)
		TAN:                "BLRA12345B",
		DeductorPAN:        "AAACD1234F",
		DeductorName:       "Payroll Test Deductor",
		FormNumber:         "24Q",
		FileCreationDate:   time.Date(2026, time.August, 5, 0, 0, 0, 0, time.UTC),
		FileSequenceNumber: 1,
		ProducerVersion:    "test-producer",
		Challans: []payrolldb.Form138ChallanInput{{
			Month:               exportMonth,
			BSRCode:             "0221826",
			DateOfDeposit:       time.Date(2026, time.August, 7, 0, 0, 0, 0, time.UTC),
			ChallanSerialNumber: 12345,
		}},
	}
}

// TestForm138AnnexureIGoldenFile pins the whole caret-delimited statement.
//
// Three things it proves that nothing else does:
//
//  1. THE BATCH HEADER'S COUNTS ARE DERIVED. One CD and one DD, and the SD
//     count is 0 because Annexure II is a Q4 filing this package refuses to
//     guess — a reader can tell no salary-detail record was dropped.
//  2. THE CHALLAN RECONCILES. The challan total was NOT supplied, so it is
//     derived from the deductee rows; payrollfile refuses any statement where
//     money deposited does not equal money credited to employees.
//  3. ONLY THE DEDUCTED APPEAR. Employee B had no deduction and has no DD
//     record; Annexure I is the statement of tax DEDUCTED, not of everyone paid.
func TestForm138AnnexureIGoldenFile(t *testing.T) {
	pool, ctx, _ := setup(t)
	svc := newService(pool)
	exportFixture(t, svc, ctx)

	var buf bytes.Buffer
	if err := svc.WriteForm138AnnexureI(ctx, &buf, form138Input()); err != nil {
		t.Fatalf("write Form 138 Annexure I: %v", err)
	}

	// TDS 16,756 for employee A is hand-derived in the fixture comment above.
	// The dates of payment and deduction are 31/07/2026 — the last day of the
	// wage month, which is the date the pay run posts its voucher on.
	want := strings.Join([]string{
		"1^FH^NS1^R^05082026^1^D^BLRA12345B^1^test-producer",
		"2^BH^1^1^24Q^Regular^0^1^BLRA12345B^AAACD1234F^202627^Q2^Payroll Test Deductor",
		"3^CD^1^1^1^N^0221826^07082026^12345^N^16756.00^0.00^0.00",
		"4^DD^1^1^1^O^1^E-EXP-A^" + exportPANA + "^Above ESI Ceiling^31072026^200000.00^16756.00^0.00^0.00^16756.00^16756.00^31072026^",
	}, "\n") + "\n"

	if got := buf.String(); got != want {
		t.Fatalf("the emitted Form 138 Annexure I is not the statement the department would be given.\n got:\n%s\nwant:\n%s\n"+
			"The deductee row is what TRACES credits to the employee's Form 130; a wrong figure here is a "+
			"tax demand addressed to the employee, not to us.", got, want)
	}
	if strings.Contains(buf.String(), "E-EXP-B") {
		t.Errorf("employee B has a deductee record; no tax was deducted from them and Annexure I is the statement of tax deducted")
	}
}

// TestForm138RefusesADeducteeWithNoPAN proves the reserved PANNOTAVBL token is
// NOT quietly substituted.
//
// payrollfile ACCEPTS the token, so substituting it would emit cleanly — and it
// would file a real person as PAN-not-available, which is assessed at the
// s.206AA rate. Changing somebody's tax treatment to make our file emit is the
// exact failure "never a silent substitute" is written against.
//
// THE PAN IS CLEARED AFTER THE RUN, WHICH IS THE ONLY WAY THIS STATE ARISES —
// and it is a realistic one. The engine refuses to compute salary tax at all
// without a PAN (s.206AA), so no payslip can be BORN without one; but the
// master is mutable and a payslip is immutable, so an operator who blanks a PAN
// they believe is wrong leaves exactly this: a posted deduction whose deductee
// can no longer be identified. The check therefore has to live at EMIT time,
// and this test is what proves it does.
func TestForm138RefusesADeducteeWithNoPAN(t *testing.T) {
	pool, ctx, _ := setup(t)
	svc := newService(pool)
	noPAN := exportEmployee(t, svc, ctx, "E-EXP-NOPAN", "No PAN Recorded", exportPANA, exportUANA, exportIPA, "")
	setStructureAndElectionAt(t, svc, ctx, noPAN, "2026-04-01", wageAboveESICeiling)
	if _, err := svc.ComputeAndRunPayRun(ctx, payrolldb.ComputePayRunInput{
		Month: exportMonth, CreatedBy: "payroll-export-test",
	}); err != nil {
		t.Fatalf("compute and run pay run: %v", err)
	}
	if err := svc.AmendEmployee(ctx, payrolldb.Employee{
		ID: noPAN, Code: "E-EXP-NOPAN", Name: "No PAN Recorded", DOJ: "2025-06-01",
		Designation: "Analyst", PAN: "", UAN: exportUANA, ESICIPNo: exportIPA,
		BankIFSC: "HDFC0001234", BankAccountLast4: "4321", IsActive: true,
	}); err != nil {
		t.Fatalf("amend employee to clear the PAN: %v", err)
	}

	var buf bytes.Buffer
	err := svc.WriteForm138AnnexureI(ctx, &buf, form138Input())
	if !errors.Is(err, payrolldb.ErrMissingStatutoryIdentifier) {
		t.Fatalf("Form 138 for a deductee with no PAN returned %v, want ErrMissingStatutoryIdentifier", err)
	}
	if strings.Contains(err.Error(), "PANNOTAVBL") && !strings.Contains(err.Error(), "deliberately NOT substituted") {
		t.Errorf("the refusal mentions the reserved token without saying it was refused: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("Form 138 wrote %d bytes while refusing", buf.Len())
	}
}

// TestForm138RefusesQ4 proves the unpublished-format refusal survives the
// mapper. Q4 carries Annexure II — every employee's whole-year taxable salary —
// and its record structure is not published.
func TestForm138RefusesQ4(t *testing.T) {
	pool, ctx, _ := setup(t)
	svc := newService(pool)
	exportFixture(t, svc, ctx)

	in := form138Input()
	in.Quarter = "Q4"
	in.Challans[0].Month = payroll.WageMonth{Year: 2027, Month: time.January}
	err := svc.WriteForm138AnnexureI(ctx, new(bytes.Buffer), in)
	if err == nil {
		t.Fatal("a Q4 Form 138 was emitted; the Q4 / Annexure II record format is not published and guessing it mis-files every employee's annual salary detail")
	}
}

// ---------------------------------------------------------------------------
// The gate that applies to all three files.
// ---------------------------------------------------------------------------

// TestStatutoryFilesRefuseADraftRun proves a statutory file can only be emitted
// from a POSTED run.
//
// The register and the payslip deliberately read ANY non-reversed run, because
// reviewing a draft before posting it is what a draft is for. A FILING is
// different: emitting one from figures that were never posted produces a return
// the books do not support, and the difference is discovered at a reconciliation
// months later.
func TestStatutoryFilesRefuseADraftRun(t *testing.T) {
	pool, ctx, _ := setup(t)
	svc := newService(pool)
	emp := exportEmployee(t, svc, ctx, "E-EXP-DRAFT", "Draft Only", exportPANA, exportUANA, exportIPB, "")
	setStructureAndElectionAt(t, svc, ctx, emp, "2026-04-01", wageWithinESICeiling)

	slips, used, err := svc.ComputeSlips(ctx, exportMonth, nil)
	if err != nil {
		t.Fatalf("compute slips: %v", err)
	}
	if _, err := svc.CreateDraftPayRun(ctx, payrolldb.PayRunInput{
		Month: exportMonth, CreatedBy: "payroll-export-test",
		Slips: slips, MonthInputs: used,
	}); err != nil {
		t.Fatalf("create draft pay run: %v", err)
	}

	// The REPORT reads the draft: that is the review step.
	if _, err := svc.PayrollRegister(ctx, exportMonth); err != nil {
		t.Fatalf("the payroll register must read a draft run (it is what a review is): %v", err)
	}
	// The FILINGS refuse it.
	for _, tc := range []struct {
		name string
		emit func() error
	}{
		{"PF ECR", func() error { return svc.WriteECR(ctx, new(bytes.Buffer), exportMonth) }},
		{"ESI return", func() error {
			return svc.WriteESIReturn(ctx, new(bytes.Buffer), exportMonth, payrolldb.ESIOptions{})
		}},
		{"Form 138", func() error {
			return svc.WriteForm138AnnexureI(ctx, new(bytes.Buffer), form138Input())
		}},
	} {
		if err := tc.emit(); !errors.Is(err, payrolldb.ErrPayRunNotPosted) {
			t.Errorf("%s emitted from a DRAFT run (err = %v); a filing produced from figures that were never "+
				"posted is a return the books cannot support", tc.name, err)
		}
	}
}

// TestSidecarCarriesTheDisclosureAndTheMappingNotes proves the sidecar is not
// decorative.
//
// None of the three formats permits an in-file comment, so this file is the
// ONLY place the "not validator-verified" statement can travel. The test pins
// the load-bearing sentences of both halves: payrollfile's disclosure, and this
// package's own statement of what the MAPPER approximated — most importantly
// that the Form 138 tax/surcharge/cess split is not recoverable from a payslip.
func TestSidecarCarriesTheDisclosureAndTheMappingNotes(t *testing.T) {
	var buf bytes.Buffer
	if err := payrolldb.WriteSidecar(&buf, "24Q_Q2_202627.txt", payrolldb.Form138Disclosures()...); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
	got := buf.String()
	for _, must := range []string{
		"24Q_Q2_202627.txt",
		"has NOT been checked by the NSDL/Protean FVU",
		"MAPPING DISCLOSURES",
		"TAX / SURCHARGE / CESS split is NOT",
		"is EXACT",
	} {
		if !strings.Contains(got, must) {
			t.Errorf("the sidecar does not carry %q. Softening or dropping it leaves an operator believing a "+
				"file was checked when nothing in this environment can check it.\n%s", must, got)
		}
	}
}
