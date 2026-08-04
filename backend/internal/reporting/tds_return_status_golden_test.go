package reporting

// THE GOLDEN THAT ENDS THE MOCK-DRIFT CLASS.
//
// ===========================================================================
// THE PROBLEM THIS SOLVES, WHICH HAD ALREADY HAPPENED TWICE
// ===========================================================================
// frontend/src/api/mock.ts hand-retypes the return_file object, and its own
// comment says it MIRRORS this package. Nothing enforced that. The consequences
// were not hypothetical:
//
//   * The mock said the Form 140 layout "has not been read from a primary
//     source" for two days after the format was committed.
//   * At the time of writing the server emitted THREE blockers and the mock
//     TWO — nobody noticed, because no test compares them.
//
// And the failure mode is the worst available: TDSRegisterScreen.test.tsx runs
// against createMockClient() and regex-matches strings that live in mock.ts. So
// when the Go wording changes and the mock does not, THE FRONTEND SUITE STAYS
// GREEN AGAINST A RETIRED REFUSAL. Every test passes and every screen lies.
//
// ===========================================================================
// THE FIX: THE MOCK STOPS PARAPHRASING AND STARTS IMPORTING
// ===========================================================================
// This test renders the real ReturnFileStatus — every string produced by
// production code, none of it retyped — and writes it to a JSON golden that
// mock.ts imports directly. The mock cannot drift from the server because it no
// longer contains a copy to drift.
//
// Regenerate deliberately, never automatically:
//
//	cd backend && UPDATE_GOLDENS=1 go test ./internal/reporting/ -run TestReturnFileStatusGolden
//
// A diff here is the test doing its job. Read what changed, decide whether the
// new wording is what you meant, and only then regenerate.
//
// ===========================================================================
// ⚠️ IT IS DELIBERATELY DATABASE-FREE, AND THAT IS THE WHOLE POINT
// ===========================================================================
// Every other test of this feature needs Postgres on :5433, and when it is down
// they call t.Skip — which `go test` reports as `ok` and exit 0. A skip is
// indistinguishable from a pass in the exit code, so a golden guarded by a DB
// connection is a golden that silently stops being checked.
//
// So this builds the status from a synthetic row instead. The cost is that it
// does not prove the SQL; four DB-backed tests in tds_return_status_test.go do
// that. What it proves is the CONTRACT — every sentence still comes out of
// buildReturnFileStatus and challanRefusalFor, so the golden is production
// prose and not a fixture somebody typed.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/shopspring/decimal"
)

// goldenPath reaches out of the Go tree into the frontend's, because the
// artefact's whole purpose is to be consumed there. Kept as one constant so the
// coupling is visible rather than buried in a string literal.
const registerProseGoldenPath = "../../../frontend/src/api/goldens/tds-register-prose.json"

// TestReturnFileStatusGolden pins the JSON the frontend mock serves.
//
// THE SCENARIO IS CHOSEN TO BE THE INSTRUCTIVE ONE, not the empty one: a
// company that HAS its deductor profile and has one deduction it has not yet
// deposited. That exercises the evidence path — the blocker naming the voucher,
// the party and the amount — which is the entire point of the feature and the
// thing a screen most needs a realistic fixture for. An all-clear or a
// no-profile fixture would leave the interesting rendering untested.
func TestReturnFileStatusGolden(t *testing.T) {
	// A synthetic deduction, deliberately matching the demo narrative the mock
	// already tells (P1, Chitra Consulting, ₹20,000 of professional fees at 10%).
	r := sourcedDeduction{
		voucherNo:  "P1",
		section:    "194J-P",
		returnCode: "4JB",
		deducteeNm: "Chitra Consulting",
		base:       decimal.RequireFromString("200000.0000"),
		rate:       decimal.RequireFromString("10.0000"),
		tax:        decimal.RequireFromString("20000.0000"),
		// challanID nil: the tax was deducted and never deposited. This is the
		// state challanRefusalFor exists to describe.
		challanID: nil,
		allocated: decimal.Zero,
	}

	// THE SENTENCE COMES FROM PRODUCTION CODE. If this returned nil the golden
	// would silently lose its evidence, so the test says so rather than writing
	// an empty blocker.
	err := challanRefusalFor(&r)
	if err == nil {
		t.Fatal("challanRefusalFor found nothing wrong with a deduction that has tax and " +
			"no challan — the golden would be generated with no evidence in it")
	}

	var undeposited returnBlockerGroup
	undeposited.add(ErrTaxNotDeposited, err)

	st := buildReturnFileStatus(true /* haveProfile */, 1, /* deductions */
		undeposited, returnBlockerGroup{}, returnBlockerGroup{})

	// THE DISCLOSURES GO IN THE SAME GOLDEN, because they are the OTHER block of
	// stale-prone prose and they render directly ABOVE this panel. When the two
	// disagreed, they disagreed in public: the disclosure asserted "this company
	// has no TAN on file" while the panel beneath it named the real blocker. Both
	// are generated from production code here, so the mock cannot hold a version
	// of either.
	//
	// TaxYearStart is set, so the two-tax-year line is deliberately absent: the
	// mock's period is one quarter of one year. The conditional lines are covered
	// by the register's own tests rather than pinned here.
	disclosures := registerDisclosures(TDSRegister{TaxYearStart: 2026})

	// Sanity, before anything is written: the invariants that must hold in every
	// golden this test could ever produce.
	if st.Available {
		t.Error("available must be false in the golden; a mock that says otherwise would " +
			"teach the screen to offer a download that does not exist")
	}
	if st.Validated {
		t.Error("validated must be false in the golden, always — the FVU cannot run here")
	}
	if st.FormatVersion == "" || st.FormatSource == "" {
		t.Error("the golden must carry the pinned version AND the document it came from; " +
			"a version with no citation behind it is not a citation")
	}

	// The shape mock.ts consumes: the two prose blocks of the register response
	// that a screen renders and a human reads.
	golden := struct {
		Disclosures []string         `json:"disclosures"`
		ReturnFile  ReturnFileStatus `json:"return_file"`
	}{Disclosures: disclosures, ReturnFile: st}

	got, err := json.MarshalIndent(golden, "", "  ")
	if err != nil {
		t.Fatalf("marshal status: %v", err)
	}
	got = append(got, '\n')

	if os.Getenv("UPDATE_GOLDENS") != "" {
		if err := os.MkdirAll(filepath.Dir(registerProseGoldenPath), 0o755); err != nil {
			t.Fatalf("mkdir golden dir: %v", err)
		}
		if err := os.WriteFile(registerProseGoldenPath, got, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("regenerated %s (%d bytes)", registerProseGoldenPath, len(got))
		return
	}

	want, err := os.ReadFile(registerProseGoldenPath)
	if err != nil {
		t.Fatalf("read golden %s: %v\n\nIf this is the first run, generate it:\n"+
			"  UPDATE_GOLDENS=1 go test ./internal/reporting/ -run TestReturnFileStatusGolden",
			registerProseGoldenPath, err)
	}
	if string(got) != string(want) {
		t.Errorf("the return_file JSON has changed, and frontend/src/api/mock.ts serves this "+
			"file to every frontend test.\n\n"+
			"THIS IS NOT A FAILURE TO ROUTE AROUND. The last two times this wording moved, "+
			"the mock did not follow and the frontend suite stayed green against a refusal "+
			"the product no longer made.\n\n"+
			"Read the diff, decide whether the new wording is what you meant, then:\n"+
			"  UPDATE_GOLDENS=1 go test ./internal/reporting/ -run TestReturnFileStatusGolden\n"+
			"and re-run the frontend suite, which asserts against it.\n\n"+
			"--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
