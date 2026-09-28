package ledger

// cost_allocation_db_test.go — THE COST-CENTRE SPLIT AGAINST THE LIVE DATABASE.
//
// ⚠️⚠️ NOT RUN IN THE AUTHORING SESSION. Every worktree on this machine shares
// ONE Postgres on :5433 and concurrent runs race, so these tests were WRITTEN and
// type-checked (go vet compiles test files) and NOT EXECUTED. Nothing below has
// been observed to pass. The integrator must run them SERIALLY:
//
//	go test ./internal/ledger/ -run 'TestParallelAllocationRoundTripsThroughPostgres|TestAlterRestatesAllocationsAgainstTheNewPostings|TestTombstoneCarriesNoCostAllocations|TestAShortAxisIsRefusedByTheDeferredTriggerAtCommit|TestCostAllocationRefusesACentreOffItsAxis' -count=1
//
// Treat a failure here as an unverified claim in cost_allocation.go, not flake.
//
// In the INTERNAL test package (`ledger`, not `ledger_test`) for the reason
// inward_doc_integration_test.go records: the DELETE guard lives inside the
// unexported post(), and DeleteVoucher builds its OWN empty VoucherInput. A test
// driven only through the public API would prove that DeleteVoucher does not
// INVENT allocations — not that a tombstone REFUSES TO RESTATE ones it is
// handed. Those are different claims and only the second is the guard.
//
// ===========================================================================
// WHAT EACH TEST IS FOR
// ===========================================================================
//   1. the parallel two-axis round trip: the ROWS and the PAYLOAD agree, and the
//      per-category invariant really does permit two complete axes at COMMIT;
//   2. the ALTER: allocations are re-derived against the NEW posting rows, not
//      attached to the superseded ones;
//   3. the tombstone: it carries none;
//   4. the deferred trigger, WITH A CONTROL LEG. A refusal that fired for the
//      wrong reason would prove nothing, and this session already had two probe
//      cases prove nothing by tripping a uniqueness constraint before reaching
//      the invariant under test.

import (
	"context"
	"errors"
	"fmt"
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

// costEnv is one isolated company with the two ledgers a pay run posts to and
// TWO cost categories with two centres each — the parallel-allocation fixture
// from migration 0040's header, made real.
type costEnv struct {
	pool    *db.Pool
	ctx     context.Context
	company uuid.UUID
	led     *Service
	ids     *identity.Service

	salaries uuid.UUID
	bank     uuid.UUID
	vtID     uuid.UUID

	// The Department axis and its two positions.
	department, sales, admin uuid.UUID
	// The Branch axis and its two positions. NOTE that a Branch centre is also
	// called "Mumbai" and a Project one could be too: cost_centre_name_unique is
	// per (company, CATEGORY, name), not per company.
	branch, mumbai, delhi uuid.UUID
}

func setupCost(t *testing.T) *costEnv {
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

	pool, err := db.New(ctx, testdb.AppDSNValue())
	if err != nil {
		t.Fatalf("app pool: %v", err)
	}
	t.Cleanup(pool.Close)

	// newInwardCompany creates the company AND registers testdb.Cleanup for it in
	// the same breath — internal/testhygiene fails the BUILD for a
	// company-creating helper that splits the pair. Reused rather than restated
	// for exactly that reason.
	companyID := newInwardCompany(t, pool, "Cost Centre Co")

	e := &costEnv{
		pool:    pool,
		ctx:     db.WithCompany(ctx, companyID),
		company: companyID,
		led:     NewService(pool),
		ids:     identity.NewService(pool),
	}
	// ⛔ FIND-OR-CREATE, NOT CREATE. "Salaries & Wages" is ALREADY SEEDED for every
	// company by migration 0034's payroll ledger set (0034:667), so a bare
	// CreateLedger trips ledger_company_id_name_key and every test in this file
	// dies in setup.
	//
	// Recorded because of WHEN it was found rather than what it was: these five
	// tests were written by a lane that was forbidden from running them (the
	// worktrees share one Postgres), so all five were handed over "green" having
	// never executed, and all five failed on the integrator's first run. That is
	// the §3.4 lesson exactly — a lane's green is a CLAIM about untested code —
	// and it is the argument for the serial integration pass rather than against
	// the lane split.
	if e.salaries, err = ensureLedger(e.ctx, e.ids, "Salaries & Wages", "Indirect Expenses"); err != nil {
		t.Fatalf("salaries ledger: %v", err)
	}
	if e.bank, err = ensureLedger(e.ctx, e.ids, "HDFC Current A/c", "Bank Accounts"); err != nil {
		t.Fatalf("bank ledger: %v", err)
	}
	if e.vtID, err = e.ids.VoucherTypeID(e.ctx, "Journal"); err != nil {
		t.Fatalf("journal voucher type: %v", err)
	}

	// The masters are seeded by SQL rather than through a service, deliberately:
	// the masters lane owns the cost-category/centre write path and this lane must
	// not depend on it having landed. What is under test here is the LEDGER SPINE.
	e.department = e.seedCategory(t, "Department")
	e.sales = e.seedCentre(t, e.department, "Sales")
	e.admin = e.seedCentre(t, e.department, "Admin")
	e.branch = e.seedCategory(t, "Branch")
	e.mumbai = e.seedCentre(t, e.branch, "Mumbai")
	e.delhi = e.seedCentre(t, e.branch, "Delhi")
	return e
}

func (e *costEnv) seedCategory(t *testing.T, name string) uuid.UUID {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new category id: %v", err)
	}
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO cost_category (id, company_id, name, created_by)
			 VALUES ($1, $2, $3, 'tester')`,
			id, e.company, name)
		return err
	}); err != nil {
		t.Fatalf("seed cost category %q: %v", name, err)
	}
	return id
}

func (e *costEnv) seedCentre(t *testing.T, category uuid.UUID, name string) uuid.UUID {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new centre id: %v", err)
	}
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO cost_centre (id, company_id, cost_category_id, name, created_by)
			 VALUES ($1, $2, $3, $4, 'tester')`,
			id, e.company, category, name)
		return err
	}); err != nil {
		t.Fatalf("seed cost centre %q: %v", name, err)
	}
	return id
}

// payRun is the worked example: Rs `amount` of salary, Dr Salaries & Wages (
// posting 0), Cr Bank (posting 1).
func (e *costEnv) payRun(amount string, allocs []CostAllocationInput) VoucherInput {
	return VoucherInput{
		Class:         Journal,
		VoucherTypeID: e.vtID,
		VoucherDate:   "2025-06-30",
		Narration:     "June salaries",
		CreatedBy:     "tester",
		Postings: []PostingInput{
			{LedgerID: e.salaries, DrCr: Debit, Amount: decimal.RequireFromString(amount)},
			{LedgerID: e.bank, DrCr: Credit, Amount: decimal.RequireFromString(amount)},
		},
		CostAllocations: allocs,
	}
}

// allocationRow is one posting_cost_allocation row as the reports will read it,
// joined out to the names an operator recognises and to the LEDGER of the posting
// it splits — which is the join that proves the posting_id mapping is right.
type allocationRow struct {
	Ledger   string
	Category string
	Centre   string
	Amount   decimal.Decimal
	// PostingAmount is the amount of the posting this allocation points at. It is
	// here so a mis-mapped posting_id is visible: an allocation of 60,000 sitting
	// on the BANK leg instead of the SALARIES leg is arithmetically fine and
	// completely wrong.
	PostingAmount decimal.Decimal
	DrCr          string
}

// currentAllocations reads the allocations of the voucher's CURRENT version.
//
// Keyed through voucher_head.current_version_id -> posting -> allocation, which
// is the ONLY correct reading: an ALTER leaves the superseded version's postings
// (and their allocations) in the log forever, and a query that ignored the head
// pointer would return both the old split and the new one and their sum would be
// nonsense.
func (e *costEnv) currentAllocations(t *testing.T, voucherID uuid.UUID) []allocationRow {
	t.Helper()
	var out []allocationRow
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT l.name, cc.name, ce.name, a.amount, p.amount, p.dr_cr::text
			   FROM voucher_head vh
			   JOIN posting p
			     ON p.company_id = vh.company_id AND p.voucher_version_id = vh.current_version_id
			   JOIN posting_cost_allocation a
			     ON a.company_id = p.company_id AND a.posting_id = p.id
			   JOIN cost_category cc ON cc.id = a.cost_category_id
			   JOIN cost_centre  ce ON ce.id = a.cost_centre_id
			   JOIN ledger       l  ON l.id  = p.ledger_id
			  WHERE vh.company_id = $1 AND vh.voucher_id = $2
			  ORDER BY cc.name, ce.name`,
			e.company, voucherID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r allocationRow
			if err := rows.Scan(&r.Ledger, &r.Category, &r.Centre, &r.Amount,
				&r.PostingAmount, &r.DrCr); err != nil {
				return err
			}
			out = append(out, r)
		}
		return rows.Err()
	}); err != nil {
		t.Fatalf("read current allocations: %v", err)
	}
	return out
}

// payloadAllocations reads the HASHED payload's allocation array of the current
// version, through jsonb operators rather than by matching on payload::text.
//
// Postgres does not preserve a jsonb column's input text — it reorders keys — so
// the stored text is NOT the hashed byte sequence and never will be. That is why
// VerifyChain re-derives the bytes by decoding back into CanonicalPayload, and
// why a test that grepped payload::text for a key order would be asserting
// something that was never true.
func (e *costEnv) payloadAllocations(t *testing.T, voucherID uuid.UUID) (present bool, rows []CanonicalCostAllocation, raw string) {
	t.Helper()
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`SELECT (vv.payload ? 'cost_allocations'), vv.payload::text
			   FROM voucher_head vh
			   JOIN voucher_version vv
			     ON vv.company_id = vh.company_id AND vv.id = vh.current_version_id
			  WHERE vh.company_id = $1 AND vh.voucher_id = $2`,
			e.company, voucherID).Scan(&present, &raw); err != nil {
			return err
		}
		if !present {
			return nil
		}
		q, err := tx.Query(ctx,
			`SELECT el ->> 'posting_seq', el ->> 'cost_category_id',
			        el ->> 'cost_centre_id', el ->> 'amount'
			   FROM voucher_head vh
			   JOIN voucher_version vv
			     ON vv.company_id = vh.company_id AND vv.id = vh.current_version_id
			   CROSS JOIN jsonb_array_elements(vv.payload -> 'cost_allocations')
			        WITH ORDINALITY AS t(el, n)
			  WHERE vh.company_id = $1 AND vh.voucher_id = $2
			  ORDER BY t.n`,
			e.company, voucherID)
		if err != nil {
			return err
		}
		defer q.Close()
		for q.Next() {
			var c CanonicalCostAllocation
			if err := q.Scan(&c.PostingSeq, &c.CostCategoryID, &c.CostCentreID, &c.Amount); err != nil {
				return err
			}
			rows = append(rows, c)
		}
		return q.Err()
	}); err != nil {
		t.Fatalf("read payload allocations: %v", err)
	}
	return present, rows, raw
}

// TestParallelAllocationRoundTripsThroughPostgres is the headline case: ONE
// posting allocated IN FULL on TWO axes, committed, and read back from BOTH
// renderings.
//
// It is the only test that can prove the deferred trigger really is grouped by
// category. The no-DB validator has its own test for that, but the validator is
// not the authority — cost_allocation_sums_to_posting is, and a trigger that
// summed posting-wide would refuse this commit no matter how correct the Go was.
// ensureLedger returns the id of a ledger, creating it only if it is not already
// there.
//
// Several of the ledgers these tests want are seeded per company by earlier
// migrations (migration 0034 seeds the whole payroll set, "Salaries & Wages"
// among them). Asking for one that exists is not an error condition to be
// tolerated -- it is the NORMAL case for a real chart of accounts, and a test
// fixture that can only run against a company nobody has enabled payroll on is a
// fixture that tests a company nobody has.
func ensureLedger(ctx context.Context, ids *identity.Service, name, group string) (uuid.UUID, error) {
	if id, err := ids.LedgerID(ctx, name); err == nil && id != uuid.Nil {
		return id, nil
	}
	id, err := ids.CreateLedger(ctx, name, group)
	if err == nil {
		return id, nil
	}
	// Lost a race, or the group differs from the seeded one: the id is what the
	// caller wants either way.
	if got, lookupErr := ids.LedgerID(ctx, name); lookupErr == nil && got != uuid.Nil {
		return got, nil
	}
	return uuid.Nil, fmt.Errorf("ensure ledger %q in %q: %w", name, group, err)
}

func TestParallelAllocationRoundTripsThroughPostgres(t *testing.T) {
	e := setupCost(t)

	// Departments: Sales 60,000 / Admin 40,000. Branches: Mumbai 70,000 /
	// Delhi 30,000. Both complete, both on posting 0 (the SALARIES debit), and
	// they total 2,00,000 across a 1,00,000 posting — which is correct, and is
	// the whole reason no report may sum across axes.
	allocs := []CostAllocationInput{
		{PostingSeq: 0, CostCategoryID: e.department, CostCentreID: e.sales, Amount: decimal.RequireFromString("60000.0000")},
		{PostingSeq: 0, CostCategoryID: e.department, CostCentreID: e.admin, Amount: decimal.RequireFromString("40000.0000")},
		{PostingSeq: 0, CostCategoryID: e.branch, CostCentreID: e.mumbai, Amount: decimal.RequireFromString("70000.0000")},
		{PostingSeq: 0, CostCategoryID: e.branch, CostCentreID: e.delhi, Amount: decimal.RequireFromString("30000.0000")},
	}
	res, err := e.led.PostVoucher(e.ctx, e.payRun("100000.0000", allocs))
	if err != nil {
		t.Fatalf("a Rs 1,00,000 salary posting allocated in full across Departments AND in full across Branches was REFUSED: %v\n\nBoth axes are complete and both are correct. If this is the deferred trigger, the sum-to-posting invariant is not grouped by category and the feature is unusable the moment a business adds a second axis.", err)
	}

	// ---- the ROWS ----------------------------------------------------------
	rows := e.currentAllocations(t, res.VoucherID)
	if len(rows) != 4 {
		t.Fatalf("expected 4 posting_cost_allocation rows, got %d: %+v", len(rows), rows)
	}
	want := []allocationRow{
		{Ledger: "Salaries & Wages", Category: "Branch", Centre: "Delhi", Amount: decimal.RequireFromString("30000")},
		{Ledger: "Salaries & Wages", Category: "Branch", Centre: "Mumbai", Amount: decimal.RequireFromString("70000")},
		{Ledger: "Salaries & Wages", Category: "Department", Centre: "Admin", Amount: decimal.RequireFromString("40000")},
		{Ledger: "Salaries & Wages", Category: "Department", Centre: "Sales", Amount: decimal.RequireFromString("60000")},
	}
	for i, w := range want {
		got := rows[i]
		if got.Category != w.Category || got.Centre != w.Centre || !got.Amount.Equal(w.Amount) {
			t.Fatalf("allocation %d is %+v, want %s/%s = %s", i, got, w.Category, w.Centre, w.Amount)
		}
		// THE MAPPING. posting_seq 0 is the SALARIES debit. An allocation that
		// landed on the Bank credit instead would satisfy the sum trigger
		// perfectly — both postings are 1,00,000 — and would report the entire
		// wage bill as a cost of the bank account. Only this assertion sees it.
		if got.Ledger != "Salaries & Wages" || got.DrCr != "D" {
			t.Fatalf("allocation %d (%s/%s) was attached to the %s %s posting, not to the Salaries & Wages debit that posting_seq 0 names. The wage bill would be reported as a cost of the wrong ledger, and because both legs are 1,00,000 the sum trigger cannot see it.",
				i, w.Category, w.Centre, got.Ledger, got.DrCr)
		}
	}

	// ---- the PAYLOAD, and that the two agree -------------------------------
	present, canon, raw := e.payloadAllocations(t, res.VoucherID)
	if !present {
		t.Fatalf("the allocations are in posting_cost_allocation but NOT in the hashed payload, so the split is a figure anything with UPDATE could move with the chain still certifying the voucher as untouched: %s", raw)
	}
	if len(canon) != 4 {
		t.Fatalf("the payload carries %d allocations but %d rows were written; the signature and the rows disagree about how the cost was split: %s", len(canon), len(rows), raw)
	}
	for i, c := range canon {
		in := allocs[i]
		if c.PostingSeq != "0" {
			t.Fatalf("payload allocation %d names posting_seq %q, not \"0\"", i, c.PostingSeq)
		}
		if c.CostCategoryID != in.CostCategoryID.String() || c.CostCentreID != in.CostCentreID.String() {
			t.Fatalf("payload allocation %d signs %s/%s but the request said %s/%s", i,
				c.CostCategoryID, c.CostCentreID, in.CostCategoryID, in.CostCentreID)
		}
		if c.Amount != in.Amount.StringFixed(4) {
			t.Fatalf("payload allocation %d signs %q but the request said %q — the amount must be a fixed-4 STRING so its scale survives the JSONB round trip",
				i, c.Amount, in.Amount.StringFixed(4))
		}
	}

	// ---- and the chain still verifies --------------------------------------
	hist, err := e.led.VoucherHistory(e.ctx, res.VoucherID)
	if err != nil {
		t.Fatalf("voucher history: %v", err)
	}
	if !hist.Verification.OK {
		t.Fatalf("the hash chain does not verify over a voucher carrying cost allocations: %s. VerifyChain re-derives the hashed bytes from the stored JSONB, so this is what a non-round-tripping field looks like — an amount that lost its scale, or a non-string field.",
			hist.Verification.Reason)
	}
}

// TestAlterRestatesAllocationsAgainstTheNewPostings is the trap this lane was
// warned about.
//
// An ALTER writes a NEW voucher_version with NEW posting rows (`posting` is HARD
// IMMUTABLE — a trigger raises on UPDATE and DELETE). The allocations must be
// re-derived against THOSE rows. If they were attached to the superseded
// postings, the correction would silently keep the old split: the operator moves
// 20,000 from Admin to Sales, the screen says it worked, and every report goes on
// showing the old numbers forever with nothing anywhere to detect it.
//
// Three claims:
//  1. the new current version's allocations are the NEW split, against the NEW
//     postings, and the new AMOUNT;
//  2. an ALTER that omits the allocations CLEARS the split — a version says what
//     the voucher is NOW, the same reading that makes "I put the 60/40 the wrong
//     way round" correctable at all;
//  3. the chain verifies across every version.
func TestAlterRestatesAllocationsAgainstTheNewPostings(t *testing.T) {
	e := setupCost(t)

	v1 := []CostAllocationInput{
		{PostingSeq: 0, CostCategoryID: e.department, CostCentreID: e.sales, Amount: decimal.RequireFromString("60000.0000")},
		{PostingSeq: 0, CostCategoryID: e.department, CostCentreID: e.admin, Amount: decimal.RequireFromString("40000.0000")},
	}
	res, err := e.led.PostVoucher(e.ctx, e.payRun("100000.0000", v1))
	if err != nil {
		t.Fatalf("post pay run: %v", err)
	}

	// ---- 1. the alter changes BOTH the amount and the split ----------------
	// The amount changes too, deliberately: if the allocations were attached to
	// the OLD postings they would still total 1,00,000 against a 1,00,000 posting
	// and the deferred trigger would be perfectly happy. Only a changed posting
	// amount makes a stale attachment observable.
	v2 := []CostAllocationInput{
		{PostingSeq: 0, CostCategoryID: e.department, CostCentreID: e.sales, Amount: decimal.RequireFromString("90000.0000")},
		{PostingSeq: 0, CostCategoryID: e.department, CostCentreID: e.admin, Amount: decimal.RequireFromString("30000.0000")},
	}
	if _, err := e.led.AlterVoucher(e.ctx, res.VoucherID, e.payRun("120000.0000", v2)); err != nil {
		t.Fatalf("alter pay run: %v", err)
	}

	rows := e.currentAllocations(t, res.VoucherID)
	if len(rows) != 2 {
		t.Fatalf("expected 2 allocations on the altered version, got %d: %+v", len(rows), rows)
	}
	byCentre := map[string]allocationRow{}
	for _, r := range rows {
		byCentre[r.Centre] = r
	}
	if got := byCentre["Sales"]; !got.Amount.Equal(decimal.RequireFromString("90000")) {
		t.Fatalf("after moving 30,000 from Admin to Sales, Sales still shows %s. The correction was written to the voucher and the allocations stayed on the SUPERSEDED postings, so every cost report keeps showing the old split forever with nothing to detect it.", got.Amount)
	}
	if got := byCentre["Admin"]; !got.Amount.Equal(decimal.RequireFromString("30000")) {
		t.Fatalf("Admin shows %s after the correction, want 30000", got.Amount)
	}
	// And they hang off the NEW posting, whose amount is 1,20,000.
	for _, r := range rows {
		if !r.PostingAmount.Equal(decimal.RequireFromString("120000")) {
			t.Fatalf("the %s allocation is attached to a posting of %s, but the current version posts 120000 — it is still hanging off the superseded posting row", r.Centre, r.PostingAmount)
		}
	}
	_, canon, raw := e.payloadAllocations(t, res.VoucherID)
	if len(canon) != 2 || canon[0].Amount != "90000.0000" {
		t.Fatalf("version 2's SIGNATURE does not carry the corrected split, so the edit log shows numbers it did not sign: %s", raw)
	}

	// ---- 2. an alter that OMITS the allocations CLEARS them ----------------
	if _, err := e.led.AlterVoucher(e.ctx, res.VoucherID, e.payRun("120000.0000", nil)); err != nil {
		t.Fatalf("alter without allocations: %v", err)
	}
	if rows := e.currentAllocations(t, res.VoucherID); len(rows) != 0 {
		t.Fatalf("an alter that omits the split must CLEAR it — otherwise a wrong departmental split can never be removed, only overwritten. Still present: %+v", rows)
	}
	present, _, raw := e.payloadAllocations(t, res.VoucherID)
	if present {
		t.Fatalf("a version with no allocations must emit NO \"cost_allocations\" key at all, or it no longer hashes like the vouchers that came before it: %s", raw)
	}

	// ---- 3. the chain verifies across all three versions -------------------
	hist, err := e.led.VoucherHistory(e.ctx, res.VoucherID)
	if err != nil {
		t.Fatalf("voucher history: %v", err)
	}
	if len(hist.Versions) != 3 {
		t.Fatalf("expected three versions, got %d", len(hist.Versions))
	}
	if !hist.Verification.OK {
		t.Fatalf("the chain no longer verifies across a voucher that gained, changed and then lost its cost allocations: %s", hist.Verification.Reason)
	}
}

// TestTombstoneCarriesNoCostAllocations pins the DELETE guard.
//
// A tombstone that restated the split would leave the deleted cost sitting on a
// department's Cost Centre Summary forever while the voucher had vanished from
// the Profit & Loss — two reports disagreeing, with nothing in either to show
// why.
//
// Driven through the unexported post() with a POPULATED VoucherInput, because
// DeleteVoucher builds an EMPTY one: going through the public API would prove
// that DeleteVoucher invents nothing, which is a different and much weaker claim
// than that the tombstone refuses what it is handed.
func TestTombstoneCarriesNoCostAllocations(t *testing.T) {
	e := setupCost(t)

	allocs := []CostAllocationInput{
		{PostingSeq: 0, CostCategoryID: e.department, CostCentreID: e.sales, Amount: decimal.RequireFromString("100000.0000")},
	}
	res, err := e.led.PostVoucher(e.ctx, e.payRun("100000.0000", allocs))
	if err != nil {
		t.Fatalf("post pay run: %v", err)
	}
	if rows := e.currentAllocations(t, res.VoucherID); len(rows) != 1 {
		t.Fatalf("the CONTROL failed: version 1 has %d allocations, not 1, so the tombstone assertion below would pass whether the guard existed or not", len(rows))
	}

	in := e.payRun("100000.0000", allocs)
	in.Postings = nil
	if _, err := e.led.post(e.ctx, res.VoucherID, OpDelete, in); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if rows := e.currentAllocations(t, res.VoucherID); len(rows) != 0 {
		t.Fatalf("the tombstone restated the cost split (%+v). The deleted cost would sit on the Sales department's summary forever while the voucher had left the Profit & Loss.", rows)
	}
	present, _, raw := e.payloadAllocations(t, res.VoucherID)
	if present {
		t.Fatalf("the tombstone SIGNED a cost split it is not supposed to carry: %s", raw)
	}
}

// TestAShortAxisIsRefusedByTheDeferredTriggerAtCommit proves the DATABASE is the
// authority, not the Go validator.
//
// It bypasses validateCostAllocations entirely by inserting the allocation rows
// directly, because that is the only way to reach the trigger: the Go check
// refuses a short axis before the transaction opens, so a test driven through
// post() would prove only that the Go check works and would say nothing about
// what happens when a future caller, an import path, or a psql session writes
// these rows.
//
// ⛔ IT HAS A CONTROL LEG, and the control comes FIRST. A refusal that fired
// because the centre was off its axis, or because a uniqueness constraint tripped,
// would look identical from here and prove nothing. So the same shape is
// committed correctly first, and only then made short.
func TestAShortAxisIsRefusedByTheDeferredTriggerAtCommit(t *testing.T) {
	e := setupCost(t)

	// Two vouchers, one per leg, so the control's committed rows cannot interfere
	// with the probe's sum (posting_cost_allocation_one_per_centre would otherwise
	// refuse the probe before the sum trigger ever ran — exactly the failure mode
	// that made two earlier probe cases in this session prove nothing).
	control, err := e.led.PostVoucher(e.ctx, e.payRun("100000.0000", nil))
	if err != nil {
		t.Fatalf("post control voucher: %v", err)
	}
	probe, err := e.led.PostVoucher(e.ctx, e.payRun("100000.0000", nil))
	if err != nil {
		t.Fatalf("post probe voucher: %v", err)
	}

	// ---- THE CONTROL: a CORRECT two-row split commits ----------------------
	if err := e.insertRawAllocations(control, [][3]any{
		{e.department, e.sales, "60000"},
		{e.department, e.admin, "40000"},
	}); err != nil {
		t.Fatalf("a CORRECT 60,000 + 40,000 split of a 1,00,000 posting was refused: %v\n\nThe probe below proves nothing unless this commits: a refusal there could then be about the centre, the axis pair, uniqueness, or RLS rather than about the sum.", err)
	}

	// ---- THE PROBE: the same shape, one rupee short -----------------------
	err = e.insertRawAllocations(probe, [][3]any{
		{e.department, e.sales, "60000"},
		{e.department, e.admin, "39999"},
	})
	if err == nil {
		t.Fatal("a Department split totalling 99,999 against a 1,00,000 posting COMMITTED. One rupee of real cost is attributed to no department, and the Cost Centre Summary will disagree with the Profit & Loss by that amount with nothing to show which axis is short. cost_allocation_sums_to_posting is the authority and it did not fire.")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("expected a Postgres error from the constraint trigger, got %T: %v", err, err)
	}
	// P0001 is raise_exception — the trigger's own RAISE. Anything else means
	// something ELSE refused the write and this test proves nothing about the sum
	// invariant. 23505 would be uniqueness, 23503 the composite FK, 23514 the
	// amount CHECK.
	if pgErr.Code != "P0001" {
		t.Fatalf("the short split was refused with SQLSTATE %s (%s), not P0001 raise_exception. Something other than cost_allocation_sums_to_posting rejected it, so this test says nothing about the per-category sum — disable the trigger and it must still fail.",
			pgErr.Code, pgErr.Message)
	}
	// The message must name the consequence, not the constraint.
	for _, want := range []string{"unattributed", "Cost Centre Summary", "Profit & Loss"} {
		if !strings.Contains(pgErr.Message, want) {
			t.Fatalf("the trigger's message must tell the operator what goes wrong in their own terms; %q is missing from: %s", want, pgErr.Message)
		}
	}

	// ---- AND THE PARALLEL CASE STILL COMMITS ------------------------------
	// A second COMPLETE axis on the control's already-allocated posting. This is
	// the leg that catches a trigger accidentally grouped posting-wide: the
	// Department axis already totals 1,00,000, so a posting-wide check would see
	// 2,00,000 and refuse — and it would refuse CORRECT data.
	if err := e.insertRawAllocations(control, [][3]any{
		{e.branch, e.mumbai, "70000"},
		{e.branch, e.delhi, "30000"},
	}); err != nil {
		t.Fatalf("a second COMPLETE axis (Branch: Mumbai 70,000 / Delhi 30,000) on a posting already fully allocated across Departments was refused: %v\n\nThat is parallel allocation and it is the entire reason cost categories exist. The trigger is grouped posting-wide instead of by (posting, category).", err)
	}
}

// insertRawAllocations writes posting_cost_allocation rows for the DEBIT posting
// of a voucher's current version, in ONE transaction, bypassing the Go validator
// entirely. Each entry is {categoryID, centreID, amount-as-string}.
//
// The whole batch commits or none of it does, which is what makes the deferred
// trigger observable: a split into three centres is three INSERTs and the first
// two are legitimately short.
func (e *costEnv) insertRawAllocations(res PostResult, rows [][3]any) error {
	return e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		var postingID uuid.UUID
		if err := tx.QueryRow(ctx,
			`SELECT p.id
			   FROM voucher_head vh
			   JOIN posting p
			     ON p.company_id = vh.company_id AND p.voucher_version_id = vh.current_version_id
			  WHERE vh.company_id = $1 AND vh.voucher_id = $2 AND p.dr_cr = 'D'`,
			e.company, res.VoucherID).Scan(&postingID); err != nil {
			return err
		}
		for _, r := range rows {
			id, err := uuid.NewV7()
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO posting_cost_allocation
				   (id, company_id, posting_id, cost_category_id, cost_centre_id, amount)
				 VALUES ($1, $2, $3, $4, $5, $6)`,
				id, e.company, postingID, r[0], r[1],
				decimal.RequireFromString(r[2].(string)),
			); err != nil {
				return err
			}
		}
		return nil
	})
}

// TestCostAllocationRefusesACentreOffItsAxis pins the composite FK — the
// constraint the Go layer deliberately does NOT duplicate, because it needs a
// lookup this package does not do.
//
// A Branch centre claimed as a Department allocation would make one axis's set
// roll up into the other's and the Category Summary would double-count. The pair
// FK is the only thing that stops it, and the control leg (the same centre under
// its OWN axis) is what proves the refusal is about the PAIR and not about the
// centre or the category individually.
func TestCostAllocationRefusesACentreOffItsAxis(t *testing.T) {
	e := setupCost(t)

	control, err := e.led.PostVoucher(e.ctx, e.payRun("100000.0000", nil))
	if err != nil {
		t.Fatalf("post control voucher: %v", err)
	}
	probe, err := e.led.PostVoucher(e.ctx, e.payRun("100000.0000", nil))
	if err != nil {
		t.Fatalf("post probe voucher: %v", err)
	}

	// CONTROL: Mumbai under its own Branch axis, complete. If this fails, the
	// probe below proves nothing.
	if err := e.insertRawAllocations(control, [][3]any{
		{e.branch, e.mumbai, "100000"},
	}); err != nil {
		t.Fatalf("Mumbai under its OWN Branch axis was refused (%v); the probe below would then prove nothing about the axis PAIR", err)
	}

	// PROBE: the SAME centre, claimed on the DEPARTMENT axis. The amount is
	// correct and the uniqueness key is fresh, so nothing but the pair FK can
	// refuse it.
	err = e.insertRawAllocations(probe, [][3]any{
		{e.department, e.mumbai, "100000"},
	})
	if err == nil {
		t.Fatal("an allocation naming the Mumbai BRANCH centre on the DEPARTMENT axis was accepted. One axis's allocation set now rolls up into the other's and the Cost Category Summary double-counts, with both figures individually plausible.")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("expected a Postgres error, got %T: %v", err, err)
	}
	if pgErr.Code != "23503" {
		t.Fatalf("refused with SQLSTATE %s (%s), not 23503 foreign_key_violation — something other than the composite (centre, category) FK rejected it, so this test says nothing about the pair. Drop the FK and it must still fail.",
			pgErr.Code, pgErr.Message)
	}
	if !strings.Contains(pgErr.ConstraintName, "centre_matches_category") {
		t.Fatalf("refused by constraint %q, which is not posting_cost_allocation_centre_matches_category_fk", pgErr.ConstraintName)
	}
}
