package masters_test

// DB-BACKED tests for the deductor return profile (migration 0070).
//
// ⚠️ WRITTEN BUT NOT EXECUTED IN THE SESSION THAT ADDED THEM: every worktree in
// that session shared one Postgres on :5433 and another process held it. They are
// part of the deliverable; the first run of `go test ./internal/masters/ -count=1`
// against a free database is the evidence. If any of them fails, read the failure
// as a real defect and not as an environment problem — none of them tolerate a
// missing database silently: setup() skips only when Postgres is unreachable, and
// the handoff's §1.3 census (`grep -c -- '--- SKIP'` must be 0) is what catches
// that.
//
// Each test gets its company from setup(), which registers testdb.Cleanup the
// moment the company exists. Nothing here calls testdb.Keep.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/masters"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/platform/testdb"
)

// companyProfile is the fixture: a Company (Annexure 4 code K) deductor, the
// branch that forbids both the Annexure 5 state name and the Annexure 3
// ministry, so each test sets exactly the field it is about.
func companyProfile() masters.TDSProfileRecord {
	return masters.TDSProfileRecord{
		TAN:          "MUMK12345E",
		PAN:          "AAACK1234M",
		DeductorName: "Khata Traders Private Limited",
		Category:     "K",
		Address: masters.TDSAddress{
			Line1:     "14 Marine Lines",
			Line2:     "Churchgate PO",
			Line3:     "Marine Drive",
			Line4:     "Fort",
			Line5:     "Mumbai City",
			Country:   "India",
			StateCode: "19",
			Pincode:   "400020",
		},
		Email:              "accounts@khata.example",
		ContactCountryCode: "91",
		ContactNumber:      "2226301234",
		GSTIN:              "27AAACK1234M1Z5",
		Responsible: masters.TDSResponsiblePerson{
			Name:        "Radhika Venkatesan",
			Designation: "Finance Director",
			PAN:         "AKQPV7821L",
			Address: masters.TDSAddress{
				Line1:     "14 Marine Lines",
				Country:   "India",
				StateCode: "19",
				Pincode:   "400020",
			},
			Email:              "radhika@khata.example",
			ContactCountryCode: "91",
			ContactNumber:      "2226301299",
		},
	}
}

// TestTDSProfileRoundTripsEveryField. The point is not that a write succeeds —
// it is that every one of the 35 columns comes back as it went in. A field that
// the binding writes to the wrong column, or that the SELECT scans in the wrong
// order, produces a return with the responsible person's pincode in the
// deductor's address and nothing anywhere fails.
func TestTDSProfileRoundTripsEveryField(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	in := companyProfile()
	if err := m.SetTDSProfile(ctx, in, "tester"); err != nil {
		t.Fatalf("set tds profile: %v", err)
	}

	got, err := m.TDSProfile(ctx)
	if err != nil {
		t.Fatalf("read tds profile: %v", err)
	}
	if got.RecordedBy != "tester" || got.RecordedAt == "" {
		t.Errorf("audit trail is %q / %q", got.RecordedBy, got.RecordedAt)
	}
	// Compare on the value, with the audit columns cleared: they are set by the
	// database and are not part of what the operator keyed.
	got.RecordedBy, got.RecordedAt = "", ""
	if got != in {
		t.Errorf("round trip differs.\n got %+v\nwant %+v", got, in)
	}
}

// TestCorrectingAProfileClearsTheFieldsTheNewCategoryForbids is the test that
// justifies the WHOLE-ROW upsert, and the one to mutate first when checking that
// this file has teeth.
//
// A State-Government deductor carries an Annexure 5 state code; a Company must
// not. Correcting S -> K must therefore CLEAR state_name_code in the same
// statement that changes the category — a partial update that set the category
// alone would violate 0070's state_name_follows_category CHECK, and one that
// forgot the column in its ON CONFLICT list would leave '19' behind and be
// rejected in exactly the same way. Either way the operator cannot correct their
// own category, which is the bug this shape prevents.
//
// MUTATION PROOF: drop `state_name_code = EXCLUDED.state_name_code` from the
// generated SET list and this test fails with the constraint name.
func TestCorrectingAProfileClearsTheFieldsTheNewCategoryForbids(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	state := companyProfile()
	state.Category = "S" // State Government
	state.StateNameCode = "19"
	state.MinistryCode = "" // forbidden for S
	if err := m.SetTDSProfile(ctx, state, "tester"); err != nil {
		t.Fatalf("record a State Government deductor: %v", err)
	}

	// The company reorganises and files as a Company. Same row, corrected.
	company := companyProfile() // K, with no state name and no ministry
	if err := m.SetTDSProfile(ctx, company, "corrector"); err != nil {
		t.Fatalf("correcting S -> K must clear the Annexure 5 code in the same statement: %v", err)
	}

	got, err := m.TDSProfile(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Category != "K" {
		t.Errorf("category is %q, want K", got.Category)
	}
	if got.StateNameCode != "" {
		t.Errorf("state_name_code is %q; a Company deductor must carry none, and a stale "+
			"value here is a field the correction did not reach", got.StateNameCode)
	}
	if got.RecordedBy != "corrector" {
		t.Errorf("recorded_by is %q; a correction records who made it", got.RecordedBy)
	}
}

// TestAGovernmentProfileCarriesItsConditionalFields — the other side of the same
// coin, so the conditional columns are proved to be writable at all rather than
// merely clearable.
func TestAGovernmentProfileCarriesItsConditionalFields(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	in := companyProfile()
	in.Category = "A" // Central Government: ministry mandatory, state name forbidden
	in.MinistryCode = "99"
	in.MinistryOther = "Ministry of Sundry Affairs"
	in.AIN = "1234567" // Government deductors paying by book entry
	if err := m.SetTDSProfile(ctx, in, "tester"); err != nil {
		t.Fatalf("record a Central Government deductor: %v", err)
	}
	got, err := m.TDSProfile(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.MinistryCode != "99" || got.MinistryOther != "Ministry of Sundry Affairs" || got.AIN != "1234567" {
		t.Errorf("conditional fields did not round trip: %+v", got)
	}
}

// TestTheAbsenceOfAProfileIsItsOwnSentinel. Migration 0070's header makes the
// absence of a row THE REFUSAL, and a screen has to tell "not filled in yet"
// apart from "filled in with blanks". An empty record would be
// indistinguishable from the second.
func TestTheAbsenceOfAProfileIsItsOwnSentinel(t *testing.T) {
	pool, ctx, _ := setup(t)
	m := masters.NewService(pool)

	if _, err := m.TDSProfile(ctx); !errors.Is(err, masters.ErrNoTDSProfile) {
		t.Fatalf("want ErrNoTDSProfile for a company with no profile, got %v", err)
	}
}

// TestTDSProfileRefusesWithoutACompanyInContext. The company is read from the
// context, never from a URL path, because RLS is what supplies tenancy and the
// GUC is set by RunInTx. A missing company must be a refusal and not a query
// against every tenant's rows.
func TestTDSProfileRefusesWithoutACompanyInContext(t *testing.T) {
	pool, _, _ := setup(t)
	m := masters.NewService(pool)

	if err := m.SetTDSProfile(context.Background(), companyProfile(), "tester"); !errors.Is(err, db.ErrNoCompany) {
		t.Errorf("write without a company: want db.ErrNoCompany, got %v", err)
	}
	if _, err := m.TDSProfile(context.Background()); !errors.Is(err, db.ErrNoCompany) {
		t.Errorf("read without a company: want db.ErrNoCompany, got %v", err)
	}
}

// TestTheProfileIsConfinedToItsCompany. This row carries a NAMED INDIVIDUAL'S
// PAN, so a read that crossed tenants would be a personal-data leak and not
// merely a tenancy bug (migration 0070 says exactly that above its policy).
//
// Two companies, one profile each, and the assertion is that neither read sees
// the other's row — with NO WHERE company_id anywhere in the store to make it
// true. RLS supplies it from app.current_company_id, which RunInTx sets as the
// transaction's first statement.
func TestTheProfileIsConfinedToItsCompany(t *testing.T) {
	pool, ctxA, _ := setup(t)
	m := masters.NewService(pool)

	// A second company in the same database, torn down like the first.
	otherID, _ := uuid.NewV7()
	ctxB := db.WithCompany(context.Background(), otherID)
	if _, err := identity.NewService(pool).CreateCompany(ctxB, identity.Company{
		ID: otherID, Name: "Other Co " + otherID.String()[:8], FYStartDate: "2025-04-01",
	}); err != nil {
		t.Fatalf("create second company: %v", err)
	}
	testdb.Cleanup(t, otherID)

	a := companyProfile()
	if err := m.SetTDSProfile(ctxA, a, "tester-a"); err != nil {
		t.Fatalf("company A profile: %v", err)
	}

	// B has none yet, and must not see A's.
	if _, err := m.TDSProfile(ctxB); !errors.Is(err, masters.ErrNoTDSProfile) {
		t.Fatalf("company B must not see company A's deductor profile: %v", err)
	}

	b := companyProfile()
	b.TAN = "DELB54321F"
	b.DeductorName = "Other Traders LLP"
	b.Responsible.Name = "Someone Else"
	if err := m.SetTDSProfile(ctxB, b, "tester-b"); err != nil {
		t.Fatalf("company B profile: %v", err)
	}

	gotA, err := m.TDSProfile(ctxA)
	if err != nil {
		t.Fatalf("read A: %v", err)
	}
	if gotA.TAN != a.TAN || gotA.Responsible.Name != a.Responsible.Name {
		t.Errorf("company A now reads %q / %q — B's write reached A's row",
			gotA.TAN, gotA.Responsible.Name)
	}
	gotB, err := m.TDSProfile(ctxB)
	if err != nil {
		t.Fatalf("read B: %v", err)
	}
	if gotB.TAN != b.TAN {
		t.Errorf("company B reads TAN %q, want %q", gotB.TAN, b.TAN)
	}

	// And exactly one row per company, which is the primary key's promise: the
	// UPSERT must never be able to append a second profile.
	if err := m.SetTDSProfile(ctxA, a, "tester-a"); err != nil {
		t.Fatalf("re-stating A's profile: %v", err)
	}
	var rows int
	if err := pool.RunInTx(ctxA, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM company_tds_profile`).Scan(&rows)
	}); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 1 {
		t.Errorf("company A can see %d profile rows, want exactly 1", rows)
	}
}

// TestTheConstraintIsStillTheAuthority. The Go checks exist to produce a
// sentence, NOT to replace the CHECKs — so this drives a value past the Go layer
// by writing it directly and asserts the database still refuses it. If this ever
// passes, migration 0070's constraint has been dropped and every sentence in
// tds_profile.go is the only thing standing between an operator and an
// unfilable return.
func TestTheConstraintIsStillTheAuthority(t *testing.T) {
	pool, ctx, companyID := setup(t)

	err := pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO company_tds_profile (
			company_id, tan, pan, deductor_name, deductor_category,
			address1, state_code, pincode, email, contact_number,
			responsible_name, responsible_designation, responsible_pan,
			responsible_address1, responsible_state_code, responsible_pincode,
			responsible_contact_number, recorded_by)
			VALUES ($1, '27AAACK1234M1Z5', 'AAACK1234M', 'Khata Traders', 'K',
			        '14 Marine Lines', '19', '400020', 'a@b.example', '2226301234',
			        'Radhika Venkatesan', 'Finance Director', 'AKQPV7821L',
			        '14 Marine Lines', '19', '400020', '2226301299', 'raw')`, companyID)
		return err
	})
	if err == nil {
		t.Fatal("a GSTIN in the tan column was accepted: migration 0070's tan_shape CHECK is gone")
	}
	if !strings.Contains(err.Error(), "company_tds_profile_tan_shape") {
		t.Errorf("refused, but not by the tan_shape constraint: %v", err)
	}
}
