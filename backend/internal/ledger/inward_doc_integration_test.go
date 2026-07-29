package ledger

// inward_doc_integration_test.go — the supplier's document, and the four tables
// migration 0032 adds, against the LIVE database.
//
// In the INTERNAL test package (`ledger`, not `ledger_test`) for the same reason
// stock_integration_test.go is: the DELETE guard for the supplier document lives
// inside the unexported post(), and DeleteVoucher builds its own empty
// VoucherInput. A test driven only through the public API would be asserting
// that DeleteVoucher does not INVENT a supplier document — not that a tombstone
// refuses to restate one it is handed. Those are different claims and only the
// second one is the guard.

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/platform/testdb"
	"github.com/khata/cored/migrations"
)

// inwardEnv is one isolated company with the two ledgers a purchase posts to.
type inwardEnv struct {
	pool    *db.Pool
	ctx     context.Context
	company uuid.UUID
	led     *Service
	ids     *identity.Service

	creditor uuid.UUID
	purchase uuid.UUID
	vtID     uuid.UUID
}

func setupInward(t *testing.T) *inwardEnv {
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

	pool, err := db.New(ctx, testdb.AppDSN)
	if err != nil {
		t.Fatalf("app pool: %v", err)
	}
	t.Cleanup(pool.Close)

	companyID := newInwardCompany(t, pool, "Inward Co")

	e := &inwardEnv{
		pool:    pool,
		ctx:     db.WithCompany(ctx, companyID),
		company: companyID,
		led:     NewService(pool),
		ids:     identity.NewService(pool),
	}
	if e.creditor, err = e.ids.CreateLedger(e.ctx, "Acme Supplies", "Sundry Creditors"); err != nil {
		t.Fatalf("create creditor: %v", err)
	}
	if e.purchase, err = e.ids.CreateLedger(e.ctx, "Local Purchases", "Purchase Accounts"); err != nil {
		t.Fatalf("create purchase ledger: %v", err)
	}
	if e.vtID, err = e.ids.VoucherTypeID(e.ctx, "Purchase"); err != nil {
		t.Fatalf("purchase voucher type: %v", err)
	}
	return e
}

// newInwardCompany creates a throwaway company and registers its teardown in the
// SAME breath. Both statements live here so that no caller can perform the first
// without the second — internal/testhygiene is a static gate that fails the
// BUILD when a company-creating function does not mention testdb, and a helper
// that split the pair would be a way to defeat it by accident.
//
// A throwaway, never a pre-existing one: Demo Traders and the tenancy company
// are LIVE fixtures the integrator's browser gates read, and a test that plants
// vouchers or ACL rows in them turns an unrelated gate red hours later with no
// trail back here.
func newInwardCompany(t *testing.T, pool *db.Pool, label string) uuid.UUID {
	t.Helper()
	companyID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new company id: %v", err)
	}
	cctx := db.WithCompany(context.Background(), companyID)
	if _, err := identity.NewService(pool).CreateCompany(cctx, identity.Company{
		ID:          companyID,
		Name:        label + " " + companyID.String()[:8],
		FYStartDate: "2025-04-01",
	}); err != nil {
		t.Fatalf("create company: %v", err)
	}
	testdb.Cleanup(t, companyID)
	return companyID
}

// currentVersionInward is what the voucher's CURRENT version records, read from
// BOTH places at once: the four columns the matcher probes, and the hashed
// payload the auditor reads.
//
// Both, deliberately. The whole design rests on the claim that the column and
// the signature are two renderings of ONE value written in ONE transaction, and
// a test that read only one of them could not tell whether that still held.
type currentVersionInward struct {
	// The columns.
	No, Date, Elig, Clause string
	// The payload, interrogated through jsonb operators rather than by string
	// matching on payload::text. Postgres does not preserve a jsonb column's
	// input text — it re-orders keys by length and then alphabetically — so the
	// stored text is NOT the hashed byte sequence and never will be. That is
	// exactly why VerifyChain re-derives the bytes by decoding back into
	// CanonicalPayload, and why a test that grepped payload::text for a key
	// order would be asserting something that was never true.
	HasInward   bool
	PayloadNo   string
	PayloadDate string
	// Raw text, for failure messages only.
	Raw string
}

func (e *inwardEnv) currentInward(t *testing.T, voucherID uuid.UUID) currentVersionInward {
	t.Helper()
	var (
		got                 currentVersionInward
		nNo, nElig, nClause *string
		pNo, pDate          *string
	)
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT vv.supplier_invoice_no,
			        COALESCE(to_char(vv.supplier_invoice_date, 'YYYY-MM-DD'), ''),
			        vv.itc_eligibility, vv.itc_blocked_clause,
			        (vv.payload ? 'inward'),
			        vv.payload -> 'inward' ->> 'supplier_invoice_no',
			        vv.payload -> 'inward' ->> 'supplier_invoice_date',
			        vv.payload::text
			   FROM voucher_head vh
			   JOIN voucher_version vv
			     ON vv.company_id = vh.company_id AND vv.id = vh.current_version_id
			  WHERE vh.company_id = $1 AND vh.voucher_id = $2`,
			e.company, voucherID).Scan(&nNo, &got.Date, &nElig, &nClause,
			&got.HasInward, &pNo, &pDate, &got.Raw)
	}); err != nil {
		t.Fatalf("read current version: %v", err)
	}
	deref := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	got.No, got.Elig, got.Clause = deref(nNo), deref(nElig), deref(nClause)
	got.PayloadNo, got.PayloadDate = deref(pNo), deref(pDate)
	return got
}

func (e *inwardEnv) purchaseInput(no string, amount string, inward *InwardDocument) VoucherInput {
	return VoucherInput{
		Class:         Purchase,
		VoucherTypeID: e.vtID,
		PartyLedgerID: e.creditor,
		VoucherNo:     no,
		VoucherDate:   "2026-03-31",
		Narration:     "Goods from Acme",
		Party:         "Acme Supplies",
		CreatedBy:     "tester",
		Postings: []PostingInput{
			{LedgerID: e.purchase, DrCr: Debit, Amount: decimal.RequireFromString(amount)},
			{LedgerID: e.creditor, DrCr: Credit, Amount: decimal.RequireFromString(amount)},
		},
		Inward: inward,
	}
}

// TestInwardDocSurvivesAlterAndIsRestated is the test the whole
// column-versus-side-table decision was made for.
//
// voucher_version is append-only: an ALTER writes a NEW version and repoints
// voucher_head.current_version_id. Anything stored ALONGSIDE a version, rather
// than ON it, therefore orphans on the first alter — the new current version
// carries no supplier document, the matcher finds no key, and the report tells
// the filer their supplier never filed. That is a false accusation manufactured
// by a schema choice, and it would appear only on AMENDED purchases, which is
// exactly the population nobody's smoke test covers.
//
// Three claims, in order:
//  1. an ALTER that restates the supplier document keeps it on the new current
//     version, in the column AND in the signature;
//  2. an ALTER that OMITS it CLEARS it — a version says what the voucher is
//     now, exactly as it does for the taxable value, and without that reading
//     "correct the invoice number I mistyped" is not expressible;
//  3. the hash chain still verifies across every version.
func TestInwardDocSurvivesAlterAndIsRestated(t *testing.T) {
	e := setupInward(t)

	doc := &InwardDocument{
		SupplierInvoiceNo:   "ACME/2025-26/0417",
		SupplierInvoiceDate: "2026-03-31",
		ITCEligibility:      "BLOCKED_17_5",
		ITCBlockedClause:    "17(5)(a)",
	}
	res, err := e.led.PostVoucher(e.ctx, e.purchaseInput("", "1000.0000", doc))
	if err != nil {
		t.Fatalf("post purchase: %v", err)
	}

	got := e.currentInward(t, res.VoucherID)
	if got.No != "ACME/2025-26/0417" || got.Date != "2026-03-31" || got.Elig != "BLOCKED_17_5" || got.Clause != "17(5)(a)" {
		t.Fatalf("version 1 did not record the supplier document in its columns: %+v", got)
	}
	// The number must be signed VERBATIM, punctuation and all. Normalisation
	// belongs to the matcher (itc.NormalizeDocNo) and happens at match time; a
	// value normalised on the way in could never be corrected afterwards,
	// because it is hashed.
	if !got.HasInward || got.PayloadNo != "ACME/2025-26/0417" || got.PayloadDate != "2026-03-31" {
		t.Fatalf("the supplier document is not in the HASHED payload of version 1, so the edit log shows a number it did not sign: %s", got.Raw)
	}

	// ---- 1. an ALTER that changes ONLY the amount keeps the document --------
	if _, err := e.led.AlterVoucher(e.ctx, res.VoucherID, e.purchaseInput("", "1200.0000", doc)); err != nil {
		t.Fatalf("alter purchase: %v", err)
	}
	got = e.currentInward(t, res.VoucherID)
	if got.No != "ACME/2025-26/0417" || got.Date != "2026-03-31" || got.Elig != "BLOCKED_17_5" || got.Clause != "17(5)(a)" {
		t.Fatalf("altering the amount LOST the supplier document from the current version (%+v). Every amended purchase would now look to the matcher like a bill the supplier never filed, and its input tax credit would be reversed.", got)
	}
	if !got.HasInward || got.PayloadNo != "ACME/2025-26/0417" {
		t.Fatalf("version 2's signature does not cover the supplier document: %s", got.Raw)
	}
	var taxable string
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT COALESCE(SUM(amount)::text, '0') FROM posting
			  WHERE company_id = $1 AND voucher_version_id =
			        (SELECT current_version_id FROM voucher_head WHERE company_id = $1 AND voucher_id = $2)
			    AND dr_cr = 'D'`,
			e.company, res.VoucherID).Scan(&taxable)
	}); err != nil {
		t.Fatalf("read altered amount: %v", err)
	}
	if !decimal.RequireFromString(taxable).Equal(decimal.RequireFromString("1200")) {
		t.Fatalf("the alter did not take effect (debit total %s); the test above proved nothing", taxable)
	}

	// ---- 2. an ALTER that OMITS the document CLEARS it ---------------------
	if _, err := e.led.AlterVoucher(e.ctx, res.VoucherID, e.purchaseInput("", "1200.0000", nil)); err != nil {
		t.Fatalf("alter without inward: %v", err)
	}
	got = e.currentInward(t, res.VoucherID)
	if got.No != "" || got.Date != "" || got.Elig != "" || got.Clause != "" {
		t.Fatalf("an alter that omits the supplier document must CLEAR it — otherwise a mistyped invoice number can never be removed, only overwritten. got %+v", got)
	}
	if got.HasInward {
		t.Fatalf("a version with no supplier document must emit no \"inward\" key at all, or it no longer hashes like the vouchers that came before it: %s", got.Raw)
	}

	// ---- 3. the chain verifies across every version ------------------------
	hist, err := e.led.VoucherHistory(e.ctx, res.VoucherID)
	if err != nil {
		t.Fatalf("voucher history: %v", err)
	}
	if len(hist.Versions) != 3 {
		t.Fatalf("expected three versions, got %d", len(hist.Versions))
	}
	if !hist.Verification.OK {
		t.Fatalf("the hash chain no longer verifies across a voucher that gained and then lost a supplier document: %s. VerifyChain re-derives the hashed bytes from the stored JSONB, so this is what a non-round-tripping field looks like.",
			hist.Verification.Reason)
	}
}

// TestTombstoneCarriesNoSupplierDocument pins the DELETE guard.
//
// A tombstone that restated the supplier's invoice number would keep matching
// against the 2B forever — the matcher reads the CURRENT version — so the filer
// would go on claiming input tax credit against a bill they had deleted from
// the books. A claim with no voucher behind it is the worst shape an ITC error
// can take, because the books cannot even show what it was for.
//
// Driven through the unexported post() with a POPULATED VoucherInput, because
// DeleteVoucher builds an empty one: going through the public API would test
// that DeleteVoucher invents nothing, which is a different and much weaker
// claim.
func TestTombstoneCarriesNoSupplierDocument(t *testing.T) {
	e := setupInward(t)

	doc := &InwardDocument{SupplierInvoiceNo: "ACME/9", SupplierInvoiceDate: "2026-03-31"}
	res, err := e.led.PostVoucher(e.ctx, e.purchaseInput("", "500.0000", doc))
	if err != nil {
		t.Fatalf("post purchase: %v", err)
	}

	in := e.purchaseInput("", "500.0000", doc)
	in.Postings = nil
	if _, err := e.led.post(e.ctx, res.VoucherID, OpDelete, in); err != nil {
		t.Fatalf("delete: %v", err)
	}

	got := e.currentInward(t, res.VoucherID)
	if got.No != "" || got.Date != "" {
		t.Fatalf("the tombstone restated the supplier document (%+v). The deleted purchase would keep matching the supplier's 2B row, and its input tax credit would be claimed against a voucher that no longer exists.", got)
	}
	if got.HasInward {
		t.Fatalf("the tombstone SIGNED a supplier document it is not supposed to carry: %s", got.Raw)
	}
}

// TestInwardTablesRefuseCrossTenantFK proves the composite (company_id, id)
// foreign keys, not RLS, are what stop a cross-tenant reference.
//
// THE GUC IS SET TO THE COMPANY THAT OWNS THE ROW BEING INSERTED, deliberately.
// With the GUC set to the other tenant, RLS's WITH CHECK would refuse the INSERT
// and the test would pass whether the composite FK existed or not — it would be
// asserting that RLS works, which 0004 already asserts. Here the row is
// perfectly legal as far as RLS is concerned and the DATABASE still refuses it,
// which is the property that survives a session GUC being set wrongly by a bug
// in the request path.
//
// Referential-integrity checks bypass row security by design, so the FK really
// does look at the other tenant's row and find no (this company, that id) pair.
func TestInwardTablesRefuseCrossTenantFK(t *testing.T) {
	e := setupInward(t)
	other := newInwardCompany(t, e.pool, "Other Co")
	otherCtx := db.WithCompany(context.Background(), other)

	// A statement that belongs to the OTHER company.
	stmtID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new statement id: %v", err)
	}
	if err := e.pool.RunInTx(otherCtx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO gst_inward_statement
			   (id, company_id, kind, return_period, source_sha256, raw, ingested_by)
			 VALUES ($1, $2, 'GSTR2B', '032026', repeat('a', 64), '{}'::jsonb, 'tester')`,
			stmtID, other)
		return err
	}); err != nil {
		t.Fatalf("seed the other tenant's statement: %v", err)
	}

	// Sanity: the SAME insert inside its own tenant succeeds, so the failure
	// below is about the tenant pair and not about the row being malformed.
	okDoc, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new doc id: %v", err)
	}
	if err := e.pool.RunInTx(otherCtx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, insertInwardDocSQL, okDoc, other, stmtID)
		return err
	}); err != nil {
		t.Fatalf("a document under its OWN statement must insert cleanly; if this fails the cross-tenant assertion below proves nothing: %v", err)
	}

	// The real assertion: our company, the other company's statement.
	badDoc, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new doc id: %v", err)
	}
	err = e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, insertInwardDocSQL, badDoc, e.company, stmtID)
		return err
	})
	if err == nil {
		t.Fatal("a gst_inward_document was accepted pointing at ANOTHER TENANT'S statement. The document would be invisible to both tenants' reports while feeding one tenant's input tax credit from the other's data.")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("expected a Postgres error, got %T: %v", err, err)
	}
	if pgErr.Code != "23503" {
		t.Fatalf("the cross-tenant reference was refused with SQLSTATE %s (%s), not 23503 foreign_key_violation. Anything else means RLS or a CHECK rejected it and the composite FK is not doing the work — remove the FK and this test must still fail.",
			pgErr.Code, pgErr.Message)
	}
	if !strings.Contains(pgErr.ConstraintName, "statement_fk") {
		t.Fatalf("refused by constraint %q, which is not the composite statement FK", pgErr.ConstraintName)
	}

	// The same property one level down: an IMS action pointing at another
	// tenant's document. This is the row that decides whether credit is claimed,
	// so a cross-tenant one is not a cosmetic leak.
	badAction, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new action id: %v", err)
	}
	err = e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO gst_ims_action (id, company_id, document_id, action, acted_at, acted_by)
			 VALUES ($1, $2, $3, 'ACCEPT', now(), 'tester')`,
			badAction, e.company, okDoc)
		return err
	})
	if err == nil {
		t.Fatal("an IMS action was accepted against ANOTHER TENANT'S document: one tenant could accept or reject the other's supplier invoices, which decides whether their credit is claimed")
	}
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
		t.Fatalf("the cross-tenant IMS action must be refused by the composite FK (23503); got %v", err)
	}
}

// insertInwardDocSQL is shared by the two halves of the cross-tenant test so the
// ONLY difference between the accepted row and the refused one is the pair of
// ids bound to it.
const insertInwardDocSQL = `
	INSERT INTO gst_inward_document
	  (id, company_id, statement_id, section, doc_no, doc_date, doc_type,
	   itc_available, taxable_value, cgst, sgst, igst)
	VALUES ($1, $2, $3, 'B2B', 'SUP/1', DATE '2026-03-31', 'I',
	        true, 1000.0000, 90.0000, 90.0000, 0)`

// TestInwardTablesAreForceRLS asserts BOTH row-security flags on all four of
// migration 0032's tables, by name.
//
// ENABLE alone is not enough and the difference is not academic. Without FORCE,
// the TABLE OWNER bypasses the policy entirely — and the owner is the role
// migrations, maintenance scripts and any admin-DSN tool connect as. A query
// that looked tenant-safe in every test would return the whole fleet the one
// time it ran as the owner, and nothing would report it, because the rows
// simply appear.
//
// Read as the SUPERUSER: the catalogue view of rowsecurity is what the flags
// actually are, independent of whatever any session can currently see.
func TestInwardTablesAreForceRLS(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, testdb.AdminDSNValue())
	if err != nil {
		t.Skipf("admin postgres unavailable: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if err := migrations.Apply(ctx, conn); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	for _, table := range []string{
		"gst_inward_statement", "gst_inward_document",
		"gst_ims_action", "gst_return_filing",
	} {
		var enabled, forced bool
		if err := conn.QueryRow(ctx,
			`SELECT relrowsecurity, relforcerowsecurity
			   FROM pg_class WHERE relname = $1 AND relkind = 'r'`,
			table).Scan(&enabled, &forced); err != nil {
			t.Fatalf("%s: read row-security flags: %v", table, err)
		}
		if !enabled {
			t.Errorf("%s does NOT have ROW LEVEL SECURITY enabled: every tenant can read every other tenant's supplier invoices", table)
		}
		if !forced {
			t.Errorf("%s has ROW LEVEL SECURITY enabled but NOT FORCED: the table owner — which is the role every migration and admin tool connects as — bypasses the tenant_isolation policy entirely, and a query that is tenant-safe under tally_app returns the whole fleet under the owner", table)
		}

		// A policy with USING but no WITH CHECK reads safely and WRITES across
		// tenants: an INSERT naming another company's id would be accepted.
		var withCheck *string
		if err := conn.QueryRow(ctx,
			`SELECT with_check FROM pg_policies
			  WHERE tablename = $1 AND policyname = 'tenant_isolation'`,
			table).Scan(&withCheck); err != nil {
			t.Fatalf("%s: read tenant_isolation policy: %v", table, err)
		}
		if withCheck == nil || !strings.Contains(*withCheck, "app_current_company_id()") {
			t.Errorf("%s's tenant_isolation policy has no WITH CHECK on app_current_company_id(): reads are scoped but a WRITE naming another company's id would be accepted", table)
		}
	}
}

// TestLedgerITCEligibilityDefaultsForEveryExistingLedger pins the set-based
// backfill. NOT NULL DEFAULT 'ELIGIBLE' is the backfill — there is no loop
// anywhere in 0032 — and 'ELIGIBLE' is the right default because it is the
// status quo: today every rupee of input tax is reported as available, so this
// column changes no existing answer until somebody classifies a ledger.
func TestLedgerITCEligibilityDefaultsForEveryExistingLedger(t *testing.T) {
	e := setupInward(t)

	var elig string
	var clause *string
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT itc_eligibility, blocked_clause FROM ledger WHERE company_id = $1 AND id = $2`,
			e.company, e.purchase).Scan(&elig, &clause)
	}); err != nil {
		t.Fatalf("read ledger eligibility: %v", err)
	}
	if elig != "ELIGIBLE" {
		t.Fatalf("a ledger created without stating its ITC treatment must default to ELIGIBLE, got %q: any other default silently changes the credit reported for every company that has not classified anything", elig)
	}
	if clause != nil {
		t.Fatalf("blocked_clause must default to NULL, got %q", *clause)
	}

	// The CHECK is the schema half of the closed vocabulary the Go half states in
	// ITCEligibilityValues(). Every listed value must be accepted and an
	// unlisted one refused, or the two halves have drifted and a voucher the API
	// accepts would be rejected by the database (or worse, the reverse).
	for _, v := range ITCEligibilityValues() {
		if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx,
				`UPDATE ledger SET itc_eligibility = $3 WHERE company_id = $1 AND id = $2`,
				e.company, e.purchase, v)
			return err
		}); err != nil {
			t.Fatalf("the Go vocabulary offers %q but the schema CHECK refuses it: %v", v, err)
		}
	}
	err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE ledger SET itc_eligibility = 'MAYBE' WHERE company_id = $1 AND id = $2`,
			e.company, e.purchase)
		return err
	})
	if err == nil {
		t.Fatal("the schema accepted an eligibility outside the closed vocabulary; the CHECK constraint is missing or wrong")
	}
}
