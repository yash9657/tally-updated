package printingdb_test

// template_test.go — the print_template override store, against the live
// database.
//
// WHAT THESE TESTS ARE PROTECTING
// ------------------------------
// Two properties, and they pull in opposite directions:
//
//	1. A company with NO row must print. The built-in default lives in code, so
//	   an absent row is the ordinary case and must return the zero overrides with
//	   no error. Any test that only ever reads rows it just wrote would leave
//	   that path — the one every new company takes — unproven.
//	2. A company must never print on another company's letterhead. That is
//	   enforced in THREE independent places (the composite foreign key, the RLS
//	   policy's WITH CHECK, and the policy's USING), and the tests below drive
//	   each of them separately, because two of the three passing is exactly how a
//	   cross-tenant hole survives a green suite.
//
// A NOTE ON THE TWO POSITIVELY-NAMED TESTS. TestCompanyBCouldStore... and
// TestCompanyBCanRead... are named for the FAILURE, not the assertion, against
// this file's own "Cannot" convention: what a reviewer sees when a tenant
// boundary breaks is a bare `--- FAIL: <name>` line in someone else's run, and
// that line has to state the breach on its own.

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/platform/testdb"
	"github.com/khata/cored/internal/printing"
	"github.com/khata/cored/internal/printing/printingdb"
	"github.com/khata/cored/migrations"
)

const appDSN = "postgres://tally_app:tally_app@localhost:5433/tally?sslmode=disable"

// setup migrates the live database (idempotent), opens an APP-ROLE pool, and
// returns a freshly created throwaway company.
//
// The pool is the app role (tally_app, NOBYPASSRLS) and never the admin one:
// under a superuser pool every RLS assertion in this file would pass for the
// wrong reason. The tests that deliberately need RLS out of the way open their
// own admin connection and say so.
//
// A THROWAWAY COMPANY EVERY TIME, never Demo Traders or the tenancy fixture:
// those are live seeds the browser gates depend on, and a test that wrote a
// template override onto them would surface as somebody else's flaky e2e run.
func setup(t *testing.T) (*db.Pool, *printingdb.Store, uuid.UUID) {
	t.Helper()
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	ctx := context.Background()

	adminConn, err := pgx.Connect(ctx, testdb.AdminDSNValue())
	if err != nil {
		t.Skipf("admin postgres unavailable, skipping integration test: %v", err)
	}
	if err := migrations.Apply(ctx, adminConn); err != nil {
		_ = adminConn.Close(ctx)
		t.Fatalf("apply migrations: %v", err)
	}
	_ = adminConn.Close(ctx)

	pool, err := db.New(ctx, appDSN)
	if err != nil {
		t.Fatalf("app pool: %v", err)
	}
	t.Cleanup(pool.Close)

	companyID := newCompany(t, pool, "Print Co")
	return pool, printingdb.NewStore(pool.Raw()), companyID
}

// newCompany creates one throwaway company and registers its teardown the moment
// it exists.
//
// testdb.Cleanup is MANDATORY: internal/testhygiene is a static gate that fails
// the build if a company-creating function does not call it. print_template
// carries a real company_id, so the schema-derived delete plan picks it up with
// no registry edit.
func newCompany(t *testing.T, pool *db.Pool, label string) uuid.UUID {
	t.Helper()
	companyID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new company id: %v", err)
	}
	cctx := db.WithCompany(context.Background(), companyID)
	if _, err := identity.NewService(pool).CreateCompany(cctx, identity.Company{
		ID:          companyID,
		Name:        label + " " + companyID.String()[:8],
		FYStartDate: "2026-04-01",
		StateCode:   "29",
	}); err != nil {
		t.Fatalf("create company: %v", err)
	}
	testdb.Cleanup(t, companyID)
	return companyID
}

// adminConn opens a SUPERUSER connection. RLS does not apply to a superuser
// (FORCE ROW LEVEL SECURITY subjects the table OWNER to its policies, not a
// superuser), so a statement issued here reaches the CONSTRAINTS with the row
// filter out of the way. That is the only way to prove a foreign key or a CHECK
// is doing work rather than the policy silently refusing first.
func adminConn(t *testing.T) *pgx.Conn {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), testdb.AdminDSNValue())
	if err != nil {
		t.Skipf("admin postgres unavailable: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

// sqlState returns the SQLSTATE of a Postgres error, or "" if err is not one.
func sqlState(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

// ---------------------------------------------------------------------------
// The absent row: the case every new company is in
// ---------------------------------------------------------------------------

// TestAFreshCompanyWithNoTemplateRowStillPrints is the load-bearing test of this
// whole slice.
//
// A company created five minutes ago has zero rows in print_template, and it
// must still be able to hand a customer a tax invoice. So an absent row is NOT
// an error and NOT a "not found": it returns the zero TemplateOverrides, which
// the renderer reads as "use the built-in default for every field". If this ever
// returns an error, a brand-new company cannot print at all — the failure would
// be discovered by a business on its first day of trading.
func TestAFreshCompanyWithNoTemplateRowStillPrints(t *testing.T) {
	_, store, companyID := setup(t)
	ctx := context.Background()

	for kind := range allKinds() {
		got, err := store.LoadOverrides(ctx, companyID, kind)
		if err != nil {
			t.Fatalf("a company with no %s template override could not print at all: %v", kind, err)
		}
		if !reflect.DeepEqual(got, printing.TemplateOverrides{}) {
			t.Errorf("a company that has customised nothing was handed overrides for %s (%+v); "+
				"the renderer would print a template this company never chose", kind, got)
		}
	}
}

// ---------------------------------------------------------------------------
// The round trip
// ---------------------------------------------------------------------------

// fullOverrides is one company's complete customisation, with every field set to
// something distinguishable so a mis-ordered scan (declaration read into terms,
// say) cannot pass.
func fullOverrides() printing.TemplateOverrides {
	return printing.TemplateOverrides{
		Title:           "TAX INVOICE (ORIGINAL FOR RECIPIENT)",
		HeaderLines:     []string{"An ISO 9001:2015 certified company", "CIN: U12345KA2020PTC123456"},
		FooterLines:     []string{"Subject to Bengaluru jurisdiction", "E. & O.E."},
		Declaration:     "Certified that the particulars given above are true and correct.",
		Terms:           "Payment due within 30 days. Interest @ 18% p.a. on overdue amounts.",
		SignatureLabel:  "For Nandi Traders Pvt Ltd — Authorised Signatory",
		ShowBankDetails: true,
		BankLines:       []string{"Bank: State Bank of India, Jayanagar", "A/c: ****4321", "IFSC: SBIN0001234"},
	}
}

// TestSavedOverridesComeBackExactlyAsTheCompanyEnteredThem round-trips every
// field, then saves again to prove the second save EDITS the row rather than
// adding a second one.
//
// The row count assertion is not incidental. Two rows for one document kind
// would make "which template printed this invoice?" unanswerable: the renderer
// would take whichever row came back first, two invoices printed minutes apart
// could carry different bank details, and afterwards there is no evidence of
// which one the customer was actually sent.
func TestSavedOverridesComeBackExactlyAsTheCompanyEnteredThem(t *testing.T) {
	pool, store, companyID := setup(t)
	ctx := context.Background()

	want := fullOverrides()
	if err := store.SaveOverrides(ctx, companyID, printing.KindTaxInvoice, want); err != nil {
		t.Fatalf("save overrides: %v", err)
	}
	got, err := store.LoadOverrides(ctx, companyID, printing.KindTaxInvoice)
	if err != nil {
		t.Fatalf("load overrides: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the invoice template this company saved is not the one it would print:\n saved: %+v\nloaded: %+v", want, got)
	}

	// A second save with one field changed: the row is EDITED.
	want.Title = "TAX INVOICE (DUPLICATE FOR TRANSPORTER)"
	want.ShowBankDetails = false
	if err := store.SaveOverrides(ctx, companyID, printing.KindTaxInvoice, want); err != nil {
		t.Fatalf("re-save overrides: %v", err)
	}
	got, err = store.LoadOverrides(ctx, companyID, printing.KindTaxInvoice)
	if err != nil {
		t.Fatalf("load overrides after re-save: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("editing the invoice template did not take effect:\n saved: %+v\nloaded: %+v", want, got)
	}

	if n := countRows(t, pool, companyID, printing.KindTaxInvoice); n != 1 {
		t.Errorf("this company now has %d tax-invoice template rows; with more than one, "+
			"which template printed a given invoice is unanswerable", n)
	}

	// Customising the invoice must not have customised anything else.
	other, err := store.LoadOverrides(ctx, companyID, printing.KindDeliveryNote)
	if err != nil {
		t.Fatalf("load delivery-note overrides: %v", err)
	}
	if !reflect.DeepEqual(other, printing.TemplateOverrides{}) {
		t.Errorf("customising the tax invoice also changed what this company's delivery notes say: %+v", other)
	}
}

// TestEveryDocumentKindTheRendererDeclaresCanBeStored drives all eight kinds
// through the store.
//
// The Go vocabulary (printingdb.knownKinds) and the SQL vocabulary (migration
// 0037's print_template_doc_kind_known CHECK) are the same closed set written
// twice. This test is what makes a disagreement between them loud: a kind the Go
// side accepts and the CHECK refuses fails the INSERT here, and a kind the Go
// side refuses fails before the round-trip. Without it, adding a ninth DocKind
// and forgetting the migration would surface as "customisation silently does not
// apply" in production.
func TestEveryDocumentKindTheRendererDeclaresCanBeStored(t *testing.T) {
	_, store, companyID := setup(t)
	ctx := context.Background()

	for kind := range allKinds() {
		o := printing.TemplateOverrides{Title: "Title for " + string(kind)}
		if err := store.SaveOverrides(ctx, companyID, kind, o); err != nil {
			t.Errorf("document kind %q cannot be stored, so a company can never customise it: %v", kind, err)
			continue
		}
		got, err := store.LoadOverrides(ctx, companyID, kind)
		if err != nil {
			t.Errorf("document kind %q cannot be read back: %v", kind, err)
			continue
		}
		if got.Title != o.Title {
			t.Errorf("document kind %q round-tripped the wrong title: %q != %q", kind, got.Title, o.Title)
		}
	}
}

// TestAnEmptyFooterOverrideIsNotTheSameAsNoFooterOverride pins the NULL / '{}'
// distinction that the array columns exist to carry.
//
// A company that asked for its footer to be SUPPRESSED (an empty override) is
// saying something different from a company that has never been asked about its
// footer (no override, print the built-in lines). If the store collapsed the two
// — storing nil as '{}', or reading '{}' back as nil — one of those two
// instructions becomes unexpressible, and the company that asked for a clean
// footer keeps printing the default one on every invoice with no way to stop it.
func TestAnEmptyFooterOverrideIsNotTheSameAsNoFooterOverride(t *testing.T) {
	pool, store, companyID := setup(t)
	ctx := context.Background()

	// (a) Suppressed: an empty, NON-NIL slice.
	suppressed := printing.TemplateOverrides{
		Title:       "BILL OF SUPPLY",
		FooterLines: []string{},
		HeaderLines: []string{},
		BankLines:   []string{},
	}
	if err := store.SaveOverrides(ctx, companyID, printing.KindBillOfSupply, suppressed); err != nil {
		t.Fatalf("save suppressed footer: %v", err)
	}
	got, err := store.LoadOverrides(ctx, companyID, printing.KindBillOfSupply)
	if err != nil {
		t.Fatalf("load suppressed footer: %v", err)
	}
	if got.FooterLines == nil {
		t.Errorf("a company that suppressed its footer got nil back, which the renderer reads as " +
			"\"use the built-in footer\" — the suppression would silently not happen")
	}
	if len(got.FooterLines) != 0 {
		t.Errorf("a suppressed footer came back with %d lines: %q", len(got.FooterLines), got.FooterLines)
	}
	// And it really is an empty ARRAY in the column, not a NULL.
	if isNull := footerIsNull(t, pool, companyID, printing.KindBillOfSupply); isNull {
		t.Errorf("the suppressed footer was stored as SQL NULL, which means \"not overridden\"; " +
			"the two instructions have been collapsed into one")
	}

	// (b) Not overridden: nil.
	notSet := printing.TemplateOverrides{Title: "DELIVERY NOTE"}
	if err := store.SaveOverrides(ctx, companyID, printing.KindDeliveryNote, notSet); err != nil {
		t.Fatalf("save absent footer: %v", err)
	}
	got, err = store.LoadOverrides(ctx, companyID, printing.KindDeliveryNote)
	if err != nil {
		t.Fatalf("load absent footer: %v", err)
	}
	if got.FooterLines != nil {
		t.Errorf("a footer that was never overridden came back as %#v; the renderer would print an "+
			"empty footer instead of the built-in one", got.FooterLines)
	}
	if isNull := footerIsNull(t, pool, companyID, printing.KindDeliveryNote); !isNull {
		t.Errorf("an un-overridden footer was stored as an empty array, which means \"print nothing\"")
	}
}

// ---------------------------------------------------------------------------
// Cross-tenant refusal, proven three ways
// ---------------------------------------------------------------------------

// TestCompanyBCouldStoreATemplateRowAgainstCompanyA is the WITH CHECK test.
//
// It issues the INSERT directly on the app pool with the tenant GUC bound to
// company B and company_id set to company A, because the Store's own API cannot
// express that mismatch (it binds the GUC from the same argument it writes into
// the column). Going around the API is the point: the guarantee has to hold at
// the DATABASE, for any statement any future caller writes, not only for the two
// methods that exist today.
//
// WHY *WITH CHECK* AND NOT JUST USING: a policy with USING alone filters reads
// and happily accepts this INSERT. Company B would then have written company A's
// invoice template — changing the declaration, the terms and the BANK DETAILS
// that print on A's invoices — and B could never see or correct the row it
// created, because USING hides it from B on the way back out. Silent
// cross-tenant contamination, discovered by A's customers paying a stranger.
func TestCompanyBCouldStoreATemplateRowAgainstCompanyA(t *testing.T) {
	pool, _, companyA := setup(t)
	companyB := newCompany(t, pool, "Other Print Co")

	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new id: %v", err)
	}

	// Scoped to B, writing A's company_id.
	err = pool.RunInTx(db.WithCompany(context.Background(), companyB),
		func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx,
				`INSERT INTO print_template (id, company_id, doc_kind, title, bank_lines, show_bank_details)
				 VALUES ($1, $2, 'tax-invoice', 'INVOICE FROM COMPANY B', $3, true)`,
				id, companyA, []string{"A/c: 9999999999 (company B's account)"})
			return err
		})
	if err == nil {
		t.Fatalf("company B stored a tax-invoice template row against company A: B has just rewritten "+
			"the declaration, terms and BANK DETAILS printed on A's invoices, and cannot see the row it "+
			"created (companyA=%s companyB=%s)", companyA, companyB)
	}
	if state := sqlState(err); state != "42501" {
		t.Errorf("the cross-tenant template write was refused, but not by the row-level-security policy "+
			"(SQLSTATE %q, want 42501): %v", state, err)
	}

	// And nothing landed: A's template is still absent, so A still prints the
	// built-in default rather than B's letterhead.
	store := printingdb.NewStore(pool.Raw())
	got, err := store.LoadOverrides(context.Background(), companyA, printing.KindTaxInvoice)
	if err != nil {
		t.Fatalf("load company A overrides: %v", err)
	}
	if !reflect.DeepEqual(got, printing.TemplateOverrides{}) {
		t.Errorf("company A's invoice template was contaminated by company B: %+v", got)
	}
}

// TestCompanyBCanReadCompanyAsInvoiceTemplate is the USING test: A's
// customisation — including its bank account — must be invisible to B.
func TestCompanyBCanReadCompanyAsInvoiceTemplate(t *testing.T) {
	pool, store, companyA := setup(t)
	companyB := newCompany(t, pool, "Other Print Co")
	ctx := context.Background()

	if err := store.SaveOverrides(ctx, companyA, printing.KindTaxInvoice, fullOverrides()); err != nil {
		t.Fatalf("company A save: %v", err)
	}

	got, err := store.LoadOverrides(ctx, companyB, printing.KindTaxInvoice)
	if err != nil {
		t.Fatalf("company B load: %v", err)
	}
	if !reflect.DeepEqual(got, printing.TemplateOverrides{}) {
		t.Errorf("company B read company A's invoice template, bank details and all: %+v", got)
	}
}

// TestATemplateRowForACompanyThatDoesNotExistIsRefusedByTheForeignKey proves the
// FOREIGN KEY refuses the row, not merely the RLS policy.
//
// It runs as a SUPERUSER, which bypasses row security entirely, so the policy
// cannot answer first and the constraint is the only thing left to refuse.
// That separation matters: RLS depends on the session GUC being set correctly by
// application code, and the FK does not. If the GUC were ever set from an
// attacker-controlled value — or simply from the wrong path segment — the FK is
// the layer still standing.
func TestATemplateRowForACompanyThatDoesNotExistIsRefusedByTheForeignKey(t *testing.T) {
	_, _, companyID := setup(t)
	conn := adminConn(t)
	ctx := context.Background()

	stranger, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new id: %v", err)
	}
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new id: %v", err)
	}

	_, err = conn.Exec(ctx,
		`INSERT INTO print_template (id, company_id, doc_kind, title)
		 VALUES ($1, $2, 'tax-invoice', 'ORPHAN TEMPLATE')`,
		id, stranger)
	if err == nil {
		// Do not leave the row behind: it belongs to no company, so no
		// company's teardown will ever remove it.
		_, _ = conn.Exec(ctx, `DELETE FROM print_template WHERE id = $1`, id)
		t.Fatalf("a template row was stored against company %s, which does not exist; such a row belongs "+
			"to no tenant, is deleted by no teardown, and would be readable by whichever company id "+
			"later collided with it", stranger)
	}
	if state := sqlState(err); state != "23503" {
		t.Errorf("the orphan template row was refused, but not by the foreign key (SQLSTATE %q, want 23503): %v",
			state, err)
	}

	// Sanity: the same INSERT for a REAL company succeeds on this connection, so
	// the refusal above was about the company id and not about the statement.
	if _, err := conn.Exec(ctx,
		`INSERT INTO print_template (id, company_id, doc_kind, title)
		 VALUES ($1, $2, 'tax-invoice', 'REAL TEMPLATE')`,
		id, companyID); err != nil {
		t.Fatalf("the control INSERT for a real company also failed, so the test above proved nothing: %v", err)
	}
}

// TestTheCompanyForeignKeyReferencesTheTenantPairAndNotJustTheId asserts the
// SHAPE of the constraint, from the catalog.
//
// WHY A CATALOG ASSERTION AND NOT AN INSERT. Every other tenant table in this
// schema can be handed a (company_id, child_id) pair that does not belong
// together, and the composite FK is what refuses it. print_template's only
// parent is the tenant ROOT, where company.company_self_scope CHECKs that
// company_id = id — so a mismatched pair cannot be constructed to insert against,
// and the usual runtime test is unavailable.
//
// The property still has to be pinned, because it is exactly the thing a future
// simplification would remove ("company_id is the same as id here, so
// REFERENCES company (id) is equivalent"). It is NOT equivalent: a single-column
// FK is only as good as the self-scope CHECK still holding, and a pair FK needs
// no such assumption. This test fails by name the moment the constraint is
// weakened to one column.
func TestTheCompanyForeignKeyReferencesTheTenantPairAndNotJustTheId(t *testing.T) {
	setup(t) // migrations applied; the company is incidental
	conn := adminConn(t)
	ctx := context.Background()

	var def string
	if err := conn.QueryRow(ctx,
		`SELECT pg_get_constraintdef(oid)
		   FROM pg_constraint
		  WHERE conname = 'print_template_company_fk'
		    AND conrelid = 'print_template'::regclass`).Scan(&def); err != nil {
		t.Fatalf("print_template has no company foreign key at all (%v); a template row could then name "+
			"a company that does not exist and no teardown would ever remove it", err)
	}
	if !strings.Contains(def, "REFERENCES company(company_id, id)") {
		t.Errorf("the company foreign key no longer references the tenant PAIR: %q\n"+
			"A single-column REFERENCES company (id) is only as strong as company_self_scope; the pair "+
			"needs no such assumption.", def)
	}

	// The CHECK the single-column form would be leaning on must also still exist.
	var checks int
	if err := conn.QueryRow(ctx,
		`SELECT count(*) FROM pg_constraint
		  WHERE conname = 'company_self_scope' AND conrelid = 'company'::regclass`).Scan(&checks); err != nil {
		t.Fatalf("read company_self_scope: %v", err)
	}
	if checks != 1 {
		t.Errorf("company.company_self_scope (company_id = id) is gone; the tenant root can now hold a row " +
			"whose company_id and id disagree, and every table scoped by company_id is affected")
	}
}

// TestATransactionWithNoTenantGUCReadsNoTemplateAtAll proves the policy is
// DEFAULT-DENY, and therefore that the GUC — not the WHERE clause — is what makes
// a template visible.
//
// app_current_company_id() returns NULL for an unset or malformed GUC, so
// `company_id = NULL` is UNKNOWN and no row qualifies. A caller that forgot to
// scope the transaction reads NOTHING; it does not read EVERYTHING. This is also
// the test that keeps printingdb's own copy of the GUC preamble honest: delete
// the set_config line from inTenantTx and every read in this file starts coming
// back empty.
func TestATransactionWithNoTenantGUCReadsNoTemplateAtAll(t *testing.T) {
	_, store, companyID := setup(t)
	ctx := context.Background()

	if err := store.SaveOverrides(ctx, companyID, printing.KindTaxInvoice, fullOverrides()); err != nil {
		t.Fatalf("save overrides: %v", err)
	}

	// A raw app-role transaction with NO GUC set. db.Pool.RunInTx cannot be used
	// here: it refuses an unscoped context by design, which is the point being
	// tested one layer down.
	raw, err := pgxpool.New(ctx, appDSN)
	if err != nil {
		t.Fatalf("raw app pool: %v", err)
	}
	defer raw.Close()

	var n int
	if err := raw.QueryRow(ctx, `SELECT count(*) FROM print_template`).Scan(&n); err != nil {
		t.Fatalf("unscoped count: %v", err)
	}
	if n != 0 {
		t.Errorf("a transaction with no tenant scope read %d template rows; an unscoped print request would "+
			"render on whatever letterhead the planner returned first", n)
	}

	// And a write with no scope is refused rather than landing unscoped.
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new id: %v", err)
	}
	if _, err := raw.Exec(ctx,
		`INSERT INTO print_template (id, company_id, doc_kind, title)
		 VALUES ($1, $2, 'receipt', 'UNSCOPED')`, id, companyID); err == nil {
		t.Errorf("a template row was written with no tenant scope; the WITH CHECK clause is not being enforced")
	} else if state := sqlState(err); state != "42501" {
		t.Errorf("the unscoped write was refused by something other than the RLS policy (SQLSTATE %q, want 42501): %v",
			state, err)
	}
}

// ---------------------------------------------------------------------------
// The closed document-kind vocabulary
// ---------------------------------------------------------------------------

// TestAnUnknownDocumentKindIsRefusedRatherThanSilentlyIgnored covers the Go half
// of the closed vocabulary.
//
// LoadOverrides returns the ZERO overrides for an absent row, so if it also
// returned the zero overrides for a MISSPELLED kind the two would be
// indistinguishable — and a caller asking for "invoice" instead of
// "tax-invoice" would be told forever, silently, that this company has no
// customisation. Its letterhead, declaration and bank details would stop
// appearing on its invoices with nothing anywhere reporting a fault.
func TestAnUnknownDocumentKindIsRefusedRatherThanSilentlyIgnored(t *testing.T) {
	store := printingdb.NewStore(nil) // no round-trip happens; the kind is checked first
	ctx := context.Background()
	anyCompany := uuid.MustParse("018f2b60-0000-7000-8000-000000000001")

	for _, bad := range []printing.DocKind{"invoice", "Tax-Invoice", "tax_invoice", "", "report:Trial Balance"} {
		if _, err := store.LoadOverrides(ctx, anyCompany, bad); !errors.Is(err, printingdb.ErrUnknownDocKind) {
			t.Errorf("LoadOverrides accepted document kind %q (err=%v); a misspelled kind must not be "+
				"indistinguishable from \"this company has no customisation\"", bad, err)
		}
		if err := store.SaveOverrides(ctx, anyCompany, bad, printing.TemplateOverrides{Title: "x"}); !errors.Is(err, printingdb.ErrUnknownDocKind) {
			t.Errorf("SaveOverrides accepted document kind %q (err=%v); the row would be stored and never "+
				"matched by any renderer", bad, err)
		}
	}

	// Every kind the renderer declares must be accepted by the same check.
	for kind := range allKinds() {
		if !printingdb.KnownKind(kind) {
			t.Errorf("printing declares document kind %q but printingdb refuses it, so it can never be customised", kind)
		}
	}
}

// TestTheDatabaseRefusesADocumentKindTheRendererCannotPrint covers the SQL half.
//
// The Go check above runs before the round-trip, so on its own it proves nothing
// about what the TABLE would accept from a future caller that skips this package
// (a psql session, a data fix, an admin tool). The CHECK constraint is that
// second guard, and this test drives it as a superuser so the RLS policy cannot
// answer first.
func TestTheDatabaseRefusesADocumentKindTheRendererCannotPrint(t *testing.T) {
	_, _, companyID := setup(t)
	conn := adminConn(t)
	ctx := context.Background()

	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new id: %v", err)
	}
	_, err = conn.Exec(ctx,
		`INSERT INTO print_template (id, company_id, doc_kind, title)
		 VALUES ($1, $2, 'invoice', 'A KIND NOTHING RENDERS')`, id, companyID)
	if err == nil {
		_, _ = conn.Exec(ctx, `DELETE FROM print_template WHERE id = $1`, id)
		t.Fatalf("the database stored a template for document kind 'invoice', which no renderer matches; " +
			"the customisation would appear saved in the UI and never apply to a printed document")
	}
	if state := sqlState(err); state != "23514" {
		t.Errorf("the unknown document kind was refused, but not by the CHECK constraint (SQLSTATE %q, want 23514): %v",
			state, err)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// allKinds is the eight DocKinds internal/printing declares, as a set. It is
// written out here (rather than read from printingdb) so that a kind DELETED
// from printingdb's own map still fails these tests instead of quietly dropping
// out of every loop.
func allKinds() map[printing.DocKind]bool {
	return map[printing.DocKind]bool{
		printing.KindTaxInvoice:   true,
		printing.KindBillOfSupply: true,
		printing.KindDeliveryNote: true,
		printing.KindReceipt:      true,
		printing.KindPayment:      true,
		printing.KindCreditNote:   true,
		printing.KindDebitNote:    true,
		printing.KindReport:       true,
	}
}

// countRows counts one company's template rows for one kind, tenant-scoped.
func countRows(t *testing.T, pool *db.Pool, companyID uuid.UUID, kind printing.DocKind) int {
	t.Helper()
	var n int
	if err := pool.RunInTx(db.WithCompany(context.Background(), companyID),
		func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx,
				`SELECT count(*) FROM print_template WHERE company_id = $1 AND doc_kind = $2`,
				companyID, string(kind)).Scan(&n)
		}); err != nil {
		t.Fatalf("count template rows: %v", err)
	}
	return n
}

// footerIsNull reports whether footer_lines is SQL NULL for one (company, kind).
// It reads the column directly because that NULL-vs-'{}' distinction is invisible
// through the Go API on its own — which is exactly the thing being asserted.
func footerIsNull(t *testing.T, pool *db.Pool, companyID uuid.UUID, kind printing.DocKind) bool {
	t.Helper()
	var isNull bool
	if err := pool.RunInTx(db.WithCompany(context.Background(), companyID),
		func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx,
				`SELECT footer_lines IS NULL FROM print_template
				  WHERE company_id = $1 AND doc_kind = $2`,
				companyID, string(kind)).Scan(&isNull)
		}); err != nil {
		t.Fatalf("read footer_lines nullness: %v", err)
	}
	return isNull
}
