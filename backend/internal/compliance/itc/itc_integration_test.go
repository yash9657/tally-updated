package itc_test

// itc_integration_test.go exercises the DB rim — store.go and service.go —
// against the live development database. The calculation core is covered by the
// in-package unit tests; what is proved here is the SQL, and above all the
// RESTATEMENT GUARD, which no pure test can reach.
//
// TEST HYGIENE: every test creates its OWN company and registers
// testdb.Cleanup(t, companyID) the moment it exists. internal/testhygiene is a
// STATIC gate that parses this file and fails the build if a function that
// creates a company does not reference testdb. Demo Traders and the tenancy
// company are LIVE and are never read or modified here.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/compliance"
	"github.com/khata/cored/internal/compliance/itc"
	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/masters"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/platform/testdb"
	"github.com/khata/cored/migrations"
)

var (
	adminDSN = testdb.AdminDSNValue()
	appDSN   = testdb.AppDSNValue()
)

const supplierState = "29" // Karnataka — the filing company's state.

// setup migrates the live DB (idempotent) and returns an app-scoped pool plus a
// freshly created, seeded company scoped into the returned context. Copied from
// gstr1_integration_test.go's setup so the two suites cannot drift on the one
// thing that matters here — that every company created is torn down.
func setup(t *testing.T) (*db.Pool, context.Context, uuid.UUID) {
	t.Helper()
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	ctx := context.Background()

	adminConn, err := pgx.Connect(ctx, adminDSN)
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

	companyID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new company id: %v", err)
	}
	cctx := db.WithCompany(ctx, companyID)

	ids := identity.NewService(pool)
	if _, err := ids.CreateCompany(cctx, identity.Company{
		ID:          companyID,
		Name:        "ITC Co " + companyID.String()[:8],
		FYStartDate: "2025-04-01",
	}); err != nil {
		t.Fatalf("create company: %v", err)
	}
	// Teardown, registered the moment the company exists.
	testdb.Cleanup(t, companyID)
	return pool, cctx, companyID
}

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// purchaseMasters is the master set the inward side needs.
type purchaseMasters struct {
	vtID                   uuid.UUID
	supplier               uuid.UUID
	purchases              uuid.UUID
	inCGST, inSGST, inIGST uuid.UUID
}

const testSupplierGSTIN = "29ABCDE1234F1Z5"

func setupMasters(t *testing.T, ms *masters.Service, ctx context.Context) purchaseMasters {
	t.Helper()
	var p purchaseMasters
	var err error
	if p.vtID, err = ms.VoucherTypeID(ctx, "Purchase"); err != nil {
		t.Fatalf("purchase voucher type: %v", err)
	}
	ensure := func(name, group string) uuid.UUID {
		t.Helper()
		id, err := ms.EnsureLedger(ctx, name, group, "tester")
		if err != nil {
			t.Fatalf("ensure ledger %s: %v", name, err)
		}
		return id
	}
	p.purchases = ensure("Local Purchases", "Purchase Accounts")
	p.inCGST = ensure("Input CGST", "Duties & Taxes")
	p.inSGST = ensure("Input SGST", "Duties & Taxes")
	p.inIGST = ensure("Input IGST", "Duties & Taxes")

	// The supplier carries a GSTIN, so its purchases are comparable.
	if p.supplier, err = ms.CreateLedger(ctx, masters.Ledger{
		Name: "Karnataka Supplies Pvt Ltd", GroupName: "Sundry Creditors",
		GSTIN: testSupplierGSTIN, StateCode: supplierState,
		RegistrationType: masters.GSTRegular,
	}, "tester"); err != nil {
		t.Fatalf("supplier ledger: %v", err)
	}
	return p
}

// postPurchase posts one INTRA-state purchase at 18% with the supplier's own
// document identity attached. It returns the voucher id.
func postPurchase(t *testing.T, led *ledger.Service, ctx context.Context, p purchaseMasters,
	voucherNo, date, taxable string, inward *ledger.InwardDocument) uuid.UUID {
	t.Helper()
	tx := dec(taxable)
	half := tx.Mul(dec("9")).Div(dec("100")).Round(4)

	res, err := led.PostVoucher(ctx, ledger.VoucherInput{
		Class:         ledger.Purchase,
		VoucherTypeID: p.vtID,
		PartyLedgerID: p.supplier,
		VoucherNo:     voucherNo,
		VoucherDate:   date,
		Party:         "Karnataka Supplies Pvt Ltd",
		CreatedBy:     "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: p.purchases, DrCr: ledger.Debit, Amount: tx},
			{LedgerID: p.inCGST, DrCr: ledger.Debit, Amount: half},
			{LedgerID: p.inSGST, DrCr: ledger.Debit, Amount: half},
			{LedgerID: p.supplier, DrCr: ledger.Credit, Amount: tx.Add(half).Add(half)},
		},
		GST: &ledger.GSTParams{
			SupplierStateCode:      supplierState,
			PlaceOfSupplyStateCode: supplierState,
			Lines:                  []compliance.Line{{TaxableValue: tx, GSTRatePct: dec("18"), HSN: "8471"}},
			CGSTLedgerID:           p.inCGST,
			SGSTLedgerID:           p.inSGST,
			IGSTLedgerID:           p.inIGST,
		},
		Inward: inward,
	})
	if err != nil {
		t.Fatalf("post purchase %s: %v", voucherNo, err)
	}
	return res.VoucherID
}

// alterPurchase re-posts an existing voucher at a new taxable value. Every
// version carries the denormalised GST columns, which is precisely why the
// books query must filter on voucher_head.current_version_id.
func alterPurchase(t *testing.T, led *ledger.Service, ctx context.Context, p purchaseMasters,
	voucherID uuid.UUID, voucherNo, date, taxable string, inward *ledger.InwardDocument) {
	t.Helper()
	tx := dec(taxable)
	half := tx.Mul(dec("9")).Div(dec("100")).Round(4)

	if _, err := led.AlterVoucher(ctx, voucherID, ledger.VoucherInput{
		Class:         ledger.Purchase,
		VoucherTypeID: p.vtID,
		PartyLedgerID: p.supplier,
		VoucherNo:     voucherNo,
		VoucherDate:   date,
		Party:         "Karnataka Supplies Pvt Ltd",
		CreatedBy:     "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: p.purchases, DrCr: ledger.Debit, Amount: tx},
			{LedgerID: p.inCGST, DrCr: ledger.Debit, Amount: half},
			{LedgerID: p.inSGST, DrCr: ledger.Debit, Amount: half},
			{LedgerID: p.supplier, DrCr: ledger.Credit, Amount: tx.Add(half).Add(half)},
		},
		GST: &ledger.GSTParams{
			SupplierStateCode:      supplierState,
			PlaceOfSupplyStateCode: supplierState,
			Lines:                  []compliance.Line{{TaxableValue: tx, GSTRatePct: dec("18"), HSN: "8471"}},
			CGSTLedgerID:           p.inCGST,
			SGSTLedgerID:           p.inSGST,
			IGSTLedgerID:           p.inIGST,
		},
		Inward: inward,
	}); err != nil {
		t.Fatalf("alter purchase %s: %v", voucherNo, err)
	}
}

// asOn is a fixed reconciliation instant, well inside the s.16(4) window for
// every FY2025-26 document these tests post and well past the Jan-2026
// deemed-acceptance cutoff.
func asOn(t *testing.T) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, "2026-03-01T10:00:00+05:30")
	if err != nil {
		t.Fatalf("parse asOn: %v", err)
	}
	return v
}

// statementJSON builds a minimal but realistic 2B for January 2026 carrying the
// named documents.
func statementJSON(gendt string, docs ...map[string]any) []byte {
	inv := make([]any, 0, len(docs))
	for _, d := range docs {
		inv = append(inv, d)
	}
	body := map[string]any{
		"data": map[string]any{
			"gstin":  "29ZZZZZ0000Z1Z5",
			"rtnprd": "012026",
			"gendt":  gendt,
			"docdata": map[string]any{
				"b2b": []any{map[string]any{
					"ctin":     testSupplierGSTIN,
					"trdnm":    "Karnataka Supplies Pvt Ltd",
					"supprd":   "012026",
					"supfildt": "11-02-2026",
					"inv":      inv,
				}},
			},
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return raw
}

func b2bDoc(inum, dt, txval, cgst, sgst string) map[string]any {
	return map[string]any{
		"inum": inum, "dt": dt, "pos": supplierState, "rev": "N", "itcavl": "Y",
		"txval": txval, "cgst": cgst, "sgst": sgst, "igst": "0.00", "cess": "0.00",
	}
}

// ---------------------------------------------------------------------------
// THE VERSION GUARD
// ---------------------------------------------------------------------------

// TestITCDecisionsAreVersionAware is the gate on the two halves of the
// restatement guard in store.go's books query.
//
// Every voucher_version carries the denormalised GST columns. Without
// `AND vh.current_version_id = vv.id` an ALTERed purchase contributes ONCE PER
// VERSION — the pre-alter 2000 AND the post-alter 5000 — and the filer claims
// credit twice on one invoice. Without `vh.is_deleted = false` a DELETEd
// purchase keeps its credit forever, because the tombstone version still has a
// row and only the head knows the voucher is gone.
//
// This is also the test gstr.checkITCDecision's Available+Ineligible partition
// depends on: a decision resolved against a superseded version carries the
// superseded version's tax.
func TestITCDecisionsAreVersionAware(t *testing.T) {
	pool, ctx, companyID := setup(t)
	ms := masters.NewService(pool)
	led := ledger.NewService(pool)
	p := setupMasters(t, ms, ctx)

	// ---- ALTER: 2000 -> 5000 ----
	inward := &ledger.InwardDocument{
		SupplierInvoiceNo:   "INV-ALT",
		SupplierInvoiceDate: "2026-01-05",
	}
	altered := postPurchase(t, led, ctx, p, "PU-1", "2026-01-05", "2000.0000", inward)
	alterPurchase(t, led, ctx, p, altered, "PU-1", "2026-01-05", "5000.0000", inward)

	// ---- DELETE ----
	deleted := postPurchase(t, led, ctx, p, "PU-2", "2026-01-07", "9000.0000",
		&ledger.InwardDocument{SupplierInvoiceNo: "INV-DEL", SupplierInvoiceDate: "2026-01-07"})
	if _, err := led.DeleteVoucher(ctx, deleted, "tester"); err != nil {
		t.Fatalf("delete voucher: %v", err)
	}

	// Sanity, read as the SUPERUSER: the altered voucher really does have two
	// live versions, so the guard has something to guard against. Without this
	// assertion the test would pass against a ledger that overwrote in place,
	// and would prove nothing.
	assertVersionCount(t, companyID, altered, 2)

	svc := itc.NewService(pool)
	rep, err := svc.Reconcile(ctx, "2026-01-01", "2026-01-31", itc.Options{AsOn: asOn(t)})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	var forAltered []itc.Line
	for _, l := range rep.Lines {
		if l.Books == nil {
			continue
		}
		if l.Books.VoucherID == deleted {
			t.Fatalf("a DELETEd purchase (%s, tax 810 per head) still contributes to the "+
				"reconciliation. The books query is missing `vh.is_deleted = false`, so "+
				"every deleted voucher's ITC is claimed forever", l.Books.VoucherNo)
		}
		if l.Books.VoucherID == altered {
			forAltered = append(forAltered, l)
		}
	}

	if len(forAltered) != 1 {
		t.Fatalf("the ALTERed purchase produced %d lines, want 1. The books query is "+
			"missing `AND vh.current_version_id = vv.id`, so BOTH versions are counted "+
			"and the filer claims credit twice on one invoice", len(forAltered))
	}

	// And it is the CURRENT version's figure: 5000 @ 18% intra = 450 per head,
	// not the superseded 2000's 180.
	got := forAltered[0].Books.Tax.CGST
	if !got.Equal(dec("450")) {
		t.Fatalf("the ALTERed purchase carries CGST %s, want 450 (5000 x 9%%). %s means "+
			"the decision was resolved against the SUPERSEDED version, and gstr's "+
			"Available+Ineligible partition would refuse the whole return",
			got, describeStale(got))
	}

	// The totals see exactly one purchase, availed in full.
	if !rep.Totals.AvailOTH.CGST.Equal(dec("450")) {
		t.Fatalf("4(A) OTH CGST = %s, want 450 — exactly the current version of the one "+
			"surviving voucher", rep.Totals.AvailOTH.CGST)
	}
}

func describeStale(got decimal.Decimal) string {
	if got.Equal(dec("180")) {
		return "180 (the pre-ALTER 2000 figure)"
	}
	if got.Equal(dec("630")) {
		return "630 (180 + 450: BOTH versions summed)"
	}
	return "that figure"
}

// assertVersionCount reads voucher_version as the SUPERUSER. tally_app is
// FORCE-RLS'd, so an ad-hoc count as the app role would silently return rows for
// one company only — which is fine here, but the superuser read is what makes
// the assertion independent of the session GUC being right.
func assertVersionCount(t *testing.T, companyID, voucherID uuid.UUID, want int) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatalf("admin connect: %v", err)
	}
	defer conn.Close(ctx)
	var n int
	if err := conn.QueryRow(ctx,
		`SELECT count(*) FROM voucher_version WHERE company_id = $1 AND voucher_id = $2`,
		companyID, voucherID).Scan(&n); err != nil {
		t.Fatalf("count versions: %v", err)
	}
	if n != want {
		t.Fatalf("voucher %s has %d versions, want %d — the fixture does not exercise "+
			"the restatement guard", voucherID, n, want)
	}
}

// ---------------------------------------------------------------------------
// Ingest and reconcile end to end
// ---------------------------------------------------------------------------

// TestIngestAndReconcile walks the whole rim: parse a 2B, store it, reconcile
// against real posted purchases, and check the classification and the two
// identities.
func TestIngestAndReconcile(t *testing.T) {
	pool, ctx, _ := setup(t)
	ms := masters.NewService(pool)
	led := ledger.NewService(pool)
	p := setupMasters(t, ms, ctx)

	// MATCHED: 10000 @ 18% -> 900 per head.
	postPurchase(t, led, ctx, p, "PU-1", "2026-01-05", "10000.0000",
		&ledger.InwardDocument{SupplierInvoiceNo: "INV-1", SupplierInvoiceDate: "2026-01-05"})
	// MISSING_IN_2B: 4000 @ 18% -> 360 per head, reversed in full.
	postPurchase(t, led, ctx, p, "PU-2", "2026-01-08", "4000.0000",
		&ledger.InwardDocument{SupplierInvoiceNo: "INV-2", SupplierInvoiceDate: "2026-01-08"})
	// NOT_COMPARABLE: no supplier document number at all — the legacy shape.
	postPurchase(t, led, ctx, p, "PU-3", "2026-01-09", "3000.0000", nil)

	svc := itc.NewService(pool)
	raw := statementJSON("14-02-2026", b2bDoc("INV-1", "05-01-2026", "10000.00", "900.00", "900.00"))
	res, err := svc.Ingest(ctx, itc.IngestInput{
		Kind: itc.KindGSTR2B, Raw: raw,
		IngestedAt: asOn(t), IngestedBy: "tester",
	})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if !res.Inserted || res.Documents != 1 || res.ReturnPeriod != "012026" {
		t.Fatalf("ingest result: %+v", res)
	}
	if len(res.Skipped) != 0 {
		t.Fatalf("the statement was partially parsed: %v", res.Skipped)
	}

	// RE-UPLOADING THE IDENTICAL FILE IS IDEMPOTENT. An operator who clicks
	// twice must not end up with two statements — the second would supersede
	// the first and the reconciliation would silently change statement id.
	res2, err := svc.Ingest(ctx, itc.IngestInput{
		Kind: itc.KindGSTR2B, Raw: raw, IngestedAt: asOn(t), IngestedBy: "tester",
	})
	if err != nil {
		t.Fatalf("re-ingest: %v", err)
	}
	if res2.Inserted {
		t.Fatalf("re-uploading the identical file created a SECOND statement (%s vs %s)",
			res2.StatementID, res.StatementID)
	}
	if res2.StatementID != res.StatementID {
		t.Fatalf("re-upload returned a different statement id")
	}

	rep, err := svc.Reconcile(ctx, "2026-01-01", "2026-01-31", itc.Options{AsOn: asOn(t)})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if rep.StatementID != res.StatementID {
		t.Fatalf("the reconciliation names statement %s, want %s — a return that cannot "+
			"name WHICH 2B produced it is not explainable", rep.StatementID, res.StatementID)
	}
	if rep.StatementGenDate == nil || rep.Resolution.IsZero() {
		t.Fatalf("gen date / resolution instant not carried: %+v", rep)
	}

	byVoucherNo := map[string]itc.Line{}
	for _, l := range rep.Lines {
		if l.Books != nil {
			byVoucherNo[l.Books.VoucherNo] = l
		}
	}
	for _, c := range []struct {
		voucherNo string
		want      itc.MatchStatus
	}{
		{"PU-1", itc.Matched},
		{"PU-2", itc.MissingIn2B},
		{"PU-3", itc.NotComparable},
	} {
		got, ok := byVoucherNo[c.voucherNo]
		if !ok {
			t.Fatalf("no line for voucher %s", c.voucherNo)
		}
		if got.Status != c.want {
			t.Fatalf("voucher %s classified %s, want %s", c.voucherNo, got.Status, c.want)
		}
	}
	if byVoucherNo["PU-3"].NotComparable != itc.NoSupplierDocNo {
		t.Fatalf("PU-3 reason %s, want %s — a purchase with no supplier document number "+
			"must not be reported as though the supplier failed to file",
			byVoucherNo["PU-3"].NotComparable, itc.NoSupplierDocNo)
	}

	// (I1): 4(A) + 4(D) == the posted input tax, head by head.
	// 900 + 360 + 270 = 1530 per head.
	posted := dec("1530")
	if got := rep.Totals.PostedTotal().CGST; !got.Equal(posted) {
		t.Fatalf("(I1) broken: 4(A)+4(D) CGST = %s, posted = %s", got, posted)
	}
	// (I2): only PU-2 is reversed. 1530 - 360 = 1170.
	if got := rep.Totals.Net().CGST; !got.Equal(dec("1170")) {
		t.Fatalf("4(C) CGST = %s, want 1170 (1530 availed less the 360 unmatched)", got)
	}

	// The disclosures say, in a machine-readable field, that this is not
	// filing-ready.
	if !containsStr(rep.Disclosures, "not-filing-ready") {
		t.Fatalf("disclosures %v must contain not-filing-ready", rep.Disclosures)
	}
	if !containsStr(rep.Disclosures, "itc-eligibility-not-classified") {
		t.Fatalf("a purchase with no supplier document number must raise "+
			"itc-eligibility-not-classified: %v", rep.Disclosures)
	}
}

func containsStr(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// TestIMSActionAndFreeze walks the action path through the DB: an action is
// appended (never updated), the latest wins, and one taken after the GSTR-3B was
// filed is refused outright.
func TestIMSActionAndFreeze(t *testing.T) {
	pool, ctx, _ := setup(t)
	ms := masters.NewService(pool)
	led := ledger.NewService(pool)
	p := setupMasters(t, ms, ctx)

	postPurchase(t, led, ctx, p, "PU-1", "2026-01-05", "10000.0000",
		&ledger.InwardDocument{SupplierInvoiceNo: "INV-1", SupplierInvoiceDate: "2026-01-05"})

	svc := itc.NewService(pool)
	if _, err := svc.Ingest(ctx, itc.IngestInput{
		Kind:       itc.KindGSTR2B,
		Raw:        statementJSON("14-02-2026", b2bDoc("INV-1", "05-01-2026", "10000.00", "900.00", "900.00")),
		IngestedAt: asOn(t), IngestedBy: "tester",
	}); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	rep, err := svc.Reconcile(ctx, "2026-01-01", "2026-01-31", itc.Options{AsOn: asOn(t)})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	var docID uuid.UUID
	for _, l := range rep.Lines {
		if l.Statement != nil {
			docID = l.Statement.ID
		}
	}
	if docID == uuid.Nil {
		t.Fatalf("no statement row in the reconciliation")
	}

	act := func(kind string, at string) error {
		ts, err := time.Parse(time.RFC3339, at)
		if err != nil {
			t.Fatalf("parse %q: %v", at, err)
		}
		return svc.RecordAction(ctx, itc.ActionInput{
			DocumentID: docID, Kind: kind, ActedAt: ts, ActedBy: "tester",
		})
	}

	if err := act(itc.ActionAccept, "2026-02-16T10:00:00+05:30"); err != nil {
		t.Fatalf("accept: %v", err)
	}
	if err := act(itc.ActionReject, "2026-02-17T10:00:00+05:30"); err != nil {
		t.Fatalf("reject: %v", err)
	}

	rep, err = svc.Reconcile(ctx, "2026-01-01", "2026-01-31", itc.Options{AsOn: asOn(t)})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	line := booksLine(t, rep, "PU-1")
	if line.IMSState != itc.Rejected {
		t.Fatalf("IMS state %s, want %s — the LATEST action must win (GSTN IMS FAQ 12)",
			line.IMSState, itc.Rejected)
	}
	if !line.Effect.RevOther.CGST.Equal(dec("900")) {
		t.Fatalf("a REJECTED document was not reversed: 4(B)(2) CGST = %s, want 900",
			line.Effect.RevOther.CGST)
	}

	// FILE THE RETURN. After the freeze instant, no further action is accepted.
	filedAt, _ := time.Parse(time.RFC3339, "2026-02-18T09:00:00+05:30")
	if err := svc.RecordFiling(ctx, itc.FormGSTR3B, "012026", filedAt, "tester"); err != nil {
		t.Fatalf("record filing: %v", err)
	}
	if err := act(itc.ActionAccept, "2026-02-20T10:00:00+05:30"); err == nil {
		t.Fatalf("an IMS action was accepted AFTER the GSTR-3B was filed. GSTN IMS FAQ 12 " +
			"freezes the action set at filing, so this software's log would disagree " +
			"with the portal's and the return would be irreproducible")
	}

	// And the projection still reports REJECTED, because the accept was refused
	// AND, had it been recorded, the freeze would exclude it.
	rep, err = svc.Reconcile(ctx, "2026-01-01", "2026-01-31", itc.Options{AsOn: asOn(t)})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if rep.Freeze == nil {
		t.Fatalf("the freeze instant was not loaded; without it the same period yields a " +
			"different answer every time it is recomputed")
	}
	if booksLine(t, rep, "PU-1").IMSState != itc.Rejected {
		t.Fatalf("IMS state after filing changed")
	}
}

func booksLine(t *testing.T, rep itc.Report, voucherNo string) itc.Line {
	t.Helper()
	for _, l := range rep.Lines {
		if l.Books != nil && l.Books.VoucherNo == voucherNo {
			return l
		}
	}
	t.Fatalf("no line for voucher %s", voucherNo)
	return itc.Line{}
}

// TestSupersedingStatement pins the append-only, never-overwritten statement
// log: a DIFFERENT file for the same period supersedes its predecessor, and the
// reconciliation follows the live one.
func TestSupersedingStatement(t *testing.T) {
	pool, ctx, _ := setup(t)
	ms := masters.NewService(pool)
	led := ledger.NewService(pool)
	p := setupMasters(t, ms, ctx)

	postPurchase(t, led, ctx, p, "PU-1", "2026-01-05", "10000.0000",
		&ledger.InwardDocument{SupplierInvoiceNo: "INV-1", SupplierInvoiceDate: "2026-01-05"})

	svc := itc.NewService(pool)
	first, err := svc.Ingest(ctx, itc.IngestInput{
		Kind: itc.KindGSTR2B,
		// The supplier had not filed yet: the first 2B is empty of our invoice.
		Raw:        statementJSON("14-02-2026", b2bDoc("INV-OTHER", "03-01-2026", "500.00", "45.00", "45.00")),
		IngestedAt: asOn(t), IngestedBy: "tester",
	})
	if err != nil {
		t.Fatalf("first ingest: %v", err)
	}

	rep, err := svc.Reconcile(ctx, "2026-01-01", "2026-01-31", itc.Options{AsOn: asOn(t)})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if booksLine(t, rep, "PU-1").Status != itc.MissingIn2B {
		t.Fatalf("against the first 2B the purchase should be MISSING_IN_2B")
	}

	// GSTN REGENERATES THE 2B (FAQ 27: recomputable without limit before
	// filing), now including our invoice.
	second, err := svc.Ingest(ctx, itc.IngestInput{
		Kind: itc.KindGSTR2B,
		Raw: statementJSON("16-02-2026",
			b2bDoc("INV-OTHER", "03-01-2026", "500.00", "45.00", "45.00"),
			b2bDoc("INV-1", "05-01-2026", "10000.00", "900.00", "900.00")),
		IngestedAt: asOn(t), IngestedBy: "tester",
	})
	if err != nil {
		t.Fatalf("second ingest: %v", err)
	}
	if !second.Inserted {
		t.Fatalf("a DIFFERENT file for the same period was treated as a duplicate")
	}
	if second.Superseded != 1 {
		t.Fatalf("the second statement superseded %d predecessors, want 1", second.Superseded)
	}
	if second.StatementID == first.StatementID {
		t.Fatalf("the second statement reused the first's id")
	}

	rep, err = svc.Reconcile(ctx, "2026-01-01", "2026-01-31", itc.Options{AsOn: asOn(t)})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if rep.StatementID != second.StatementID {
		t.Fatalf("the reconciliation still names the SUPERSEDED statement %s", rep.StatementID)
	}
	line := booksLine(t, rep, "PU-1")
	if line.Status != itc.Matched {
		t.Fatalf("against the regenerated 2B the purchase is %s, want %s", line.Status, itc.Matched)
	}
	if !line.Effect.RevOther.IsZero() {
		t.Fatalf("a now-matched purchase is still reversed: %v", line.Effect.RevOther)
	}
	// The regenerated statement's OWN gen date drives the resolution instant —
	// not the computed 14th, which has already slipped once in real life.
	want, _ := time.Parse(time.RFC3339, "2026-02-16T00:00:00+05:30")
	if !rep.Resolution.Equal(want) {
		t.Fatalf("resolution instant %s, want the statement's own gen date %s",
			rep.Resolution.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

// TestReconcileRefusesWithoutAnAsOnInstant pins the no-wall-clock rule at the
// service boundary: this package never calls time.Now(), so a caller that
// forgets the clock gets an error rather than a silently-current answer.
func TestReconcileRefusesWithoutAnAsOnInstant(t *testing.T) {
	pool, ctx, _ := setup(t)
	svc := itc.NewService(pool)
	if _, err := svc.Reconcile(ctx, "2026-01-01", "2026-01-31", itc.Options{}); err == nil {
		t.Fatalf("Reconcile accepted an Options with no AsOn. A boundary that reads the " +
			"wall clock cannot be tested at the boundary, and every boundary here " +
			"decides which TAX PERIOD a credit lands in")
	}
	if _, err := svc.Decisions(ctx, "2026-01-01", "2026-01-31"); err == nil {
		t.Fatalf("Decisions accepted a context with no as-on instant")
	}
	// With the instant on the context, the gstr.ITCSource path works.
	decs, err := svc.Decisions(itc.WithAsOn(ctx, asOn(t)), "2026-01-01", "2026-01-31")
	if err != nil {
		t.Fatalf("Decisions: %v", err)
	}
	if len(decs) != 0 {
		t.Fatalf("an empty company produced %d decisions", len(decs))
	}
}

// TestResidueIsZero is the belt-and-braces check that this suite's own fixtures
// are torn down. It runs LAST by name and asserts only what it created.
func TestResidueIsZero(t *testing.T) {
	pool, ctx, companyID := setup(t)
	ms := masters.NewService(pool)
	led := ledger.NewService(pool)
	p := setupMasters(t, ms, ctx)
	postPurchase(t, led, ctx, p, "PU-1", "2026-01-05", "1000.0000",
		&ledger.InwardDocument{SupplierInvoiceNo: "INV-1", SupplierInvoiceDate: "2026-01-05"})

	svc := itc.NewService(pool)
	if _, err := svc.Ingest(ctx, itc.IngestInput{
		Kind:       itc.KindGSTR2B,
		Raw:        statementJSON("14-02-2026", b2bDoc("INV-1", "05-01-2026", "1000.00", "90.00", "90.00")),
		IngestedAt: asOn(t), IngestedBy: "tester",
	}); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	// The rows exist NOW. testdb.Cleanup removes them when the test ends; this
	// assertion is what proves the fixture was real, so a teardown that silently
	// did nothing cannot look identical to a test that created nothing.
	for _, tbl := range []string{"gst_inward_statement", "gst_inward_document"} {
		if n := countRows(t, tbl, companyID); n == 0 {
			t.Fatalf("%s has no rows for this company; the fixture did not write", tbl)
		}
	}
}

func countRows(t *testing.T, table string, companyID uuid.UUID) int {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatalf("admin connect: %v", err)
	}
	defer conn.Close(ctx)
	var n int
	if err := conn.QueryRow(ctx,
		fmt.Sprintf(`SELECT count(*) FROM %s WHERE company_id = $1`, table), companyID).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}
