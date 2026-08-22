package masters_test

// D93 — every refusal an HTTP caller can provoke on CreateLedger must be a
// SENTENCE, never a database error.
//
// ⛔ HOW THIS WAS FOUND: a mutation proof for D87, and by the PREDICTION BEING
// WRONG. Sending the display label "Regular" instead of the enum value
// "REGULAR" was expected to be silently coerced to the UNKNOWN default. It is
// not — the INSERT fails the enum cast — so the refusal was correct all along
// and only its SENTENCE was wrong:
//
//	masters: insert ledger: ERROR: invalid input value for enum
//	gst_registration_type: "Regular" (SQLSTATE 22P02)
//
// Two CHECK constraints leaked the same way, and worse: they named the
// constraint. `ledger_bank_ifsc_shape` and `ledger_bank_account_number_not_blank`
// went to the caller verbatim.
//
// ⚠️ THE EXPECTATION WAS ALREADY PINNED — ON ONE PATH. drive-l13-masters.mjs
// asserts `!/SQLSTATE/` on a refusal from this very route (the duplicate-name
// arm), so three sibling refusals were unenforced beside one that was.

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/masters"
	"github.com/khata/cored/internal/platform/db"
)

// newLedgerService reuses this package's own `setup`, which applies the
// migrations, opens the app pool, creates a scoped company and registers its
// teardown. Nothing here needs a second harness.
func newLedgerService(t *testing.T) (*masters.Service, context.Context, func()) {
	t.Helper()
	pool, ctx, companyID := setup(t)
	return masters.NewService(pool), db.WithCompany(ctx, companyID), func() {}
}

// TestCreateLedgerRefusesWithSentencesNotDatabaseErrors drives every body an
// HTTP caller can send that the DATABASE would otherwise reject.
func TestCreateLedgerRefusesWithSentencesNotDatabaseErrors(t *testing.T) {
	svc, ctx, cleanup := newLedgerService(t)
	defer cleanup()

	for _, tc := range []struct {
		name   string
		ledger masters.Ledger
		want   string
	}{
		{
			name: "a registration type that is not in the enum",
			ledger: masters.Ledger{
				Name: "Enum " + uuid.NewString()[:8], GroupName: "Sundry Debtors",
				RegistrationType: masters.GSTRegistrationType("Regular"),
			},
			// The message must name what IS allowed. "Invalid" alone sends the
			// caller to read the schema.
			want: "is not a GST registration type",
		},
		{
			name: "an IFSC that does not match the bank's own shape",
			ledger: masters.Ledger{
				Name: "IFSC " + uuid.NewString()[:8], GroupName: "Bank Accounts",
				BankIFSC: "nope",
			},
			want: "is not an IFSC",
		},
		{
			name: "a bank account number of only spaces",
			ledger: masters.Ledger{
				Name: "Acct " + uuid.NewString()[:8], GroupName: "Bank Accounts",
				BankAccountNumber: "   ",
			},
			want: "cannot be only spaces",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.CreateLedger(ctx, tc.ledger, "test")
			if err == nil {
				t.Fatal("the create was ACCEPTED; it must be refused")
			}
			msg := err.Error()
			if !strings.Contains(msg, tc.want) {
				t.Errorf("message = %q, want it to contain %q", msg, tc.want)
			}
			// ⛔ THE ASSERTION THAT MAKES THIS A D93 TEST AND NOT A DUPLICATE OF
			// THE ONE ABOVE. A refusal can carry the right words and still leak
			// the database underneath them.
			for _, leak := range []string{"SQLSTATE", "insert ledger", "constraint", "relation"} {
				if strings.Contains(msg, leak) {
					t.Errorf("refusal leaks %q to the caller: %q", leak, msg)
				}
			}
		})
	}
}

// TestCreateLedgerStillAcceptsEveryRealRegistrationType is the control.
//
// ⛔ WITHOUT IT, "refuse everything" PASSES the test above. The validation is
// only correct if all six enum members still get through — including UNKNOWN,
// which is the column default and therefore what an omitted field becomes, even
// though the ledger FORM deliberately does not offer it as a choice (D87).
func TestCreateLedgerStillAcceptsEveryRealRegistrationType(t *testing.T) {
	svc, ctx, cleanup := newLedgerService(t)
	defer cleanup()

	for _, rt := range []masters.GSTRegistrationType{
		masters.GSTUnregistered, masters.GSTRegular, masters.GSTComposition,
		masters.GSTConsumer, masters.GSTSEZ, masters.GSTUnknown,
	} {
		t.Run(string(rt), func(t *testing.T) {
			if _, err := svc.CreateLedger(ctx, masters.Ledger{
				Name:      string(rt) + " " + uuid.NewString()[:8],
				GroupName: "Sundry Debtors",
				// A GSTIN is not required by the service; the FORM demands one
				// for Regular/Composition, which is a different (client-side)
				// rule and deliberately not duplicated here.
				RegistrationType: rt,
			}, "test"); err != nil {
				t.Errorf("%s was refused: %v", rt, err)
			}
		})
	}
	// And an EMPTY type still defaults rather than being refused — a body that
	// omits the field is the commonest one in the product.
	if _, err := svc.CreateLedger(ctx, masters.Ledger{
		Name: "Omitted " + uuid.NewString()[:8], GroupName: "Sundry Debtors",
	}, "test"); err != nil {
		t.Errorf("an omitted registration type was refused: %v", err)
	}
}
