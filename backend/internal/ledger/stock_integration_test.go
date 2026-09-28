package ledger

// stock_integration_test.go — the inventory plane against the LIVE database.
//
// These are in the internal test package (`ledger`, not `ledger_test`) for one
// reason: post()'s OpDelete guard for stock is only reachable through the
// unexported post(). DeleteVoucher constructs its own empty VoucherInput, so a
// test driven purely through the public API would pass whether the guard exists
// or not — it would be testing that DeleteVoucher does not invent stock lines,
// not that a tombstone refuses to write the ones it is handed.

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/platform/testdb"
	"github.com/khata/cored/migrations"
)

var (
	stockAdminDSN = testdb.AdminDSNValue()
	stockAppDSN   = testdb.AppDSNValue()
)

// stockEnv is one isolated company with everything an inventory voucher needs:
// the two ledgers a purchase posts to, the seeded 'Main Location' godown, a
// stock item, and a batch under it.
type stockEnv struct {
	pool    *db.Pool
	ctx     context.Context
	company uuid.UUID
	led     *Service

	creditor uuid.UUID
	purchase uuid.UUID
	godown   uuid.UUID
	item     uuid.UUID
	batch    uuid.UUID
}

func setupStock(t *testing.T) *stockEnv {
	t.Helper()
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	ctx := context.Background()

	adminConn, err := pgx.Connect(ctx, stockAdminDSN)
	if err != nil {
		t.Skipf("admin postgres unavailable, skipping integration test: %v", err)
	}
	if err := migrations.Apply(ctx, adminConn); err != nil {
		_ = adminConn.Close(ctx)
		t.Fatalf("apply migrations: %v", err)
	}
	_ = adminConn.Close(ctx)

	pool, err := db.New(ctx, stockAppDSN)
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
		Name:        "Stock Co " + companyID.String()[:8],
		FYStartDate: "2025-04-01",
	}); err != nil {
		t.Fatalf("create company: %v", err)
	}
	// Teardown, registered before the ledgers, items and batches below. This
	// harness is the heaviest leaker in the package: every stock test writes
	// stock_movement rows, and stock_movement carries a BEFORE UPDATE OR DELETE
	// immutability trigger, so nothing short of testdb's replica-mode transaction
	// can remove them at all.
	testdb.Cleanup(t, companyID)

	e := &stockEnv{pool: pool, ctx: cctx, company: companyID, led: NewService(pool)}
	if e.creditor, err = ids.CreateLedger(cctx, "Acme Supplies", "Sundry Creditors"); err != nil {
		t.Fatalf("create creditor: %v", err)
	}
	if e.purchase, err = ids.CreateLedger(cctx, "Local Purchases", "Purchase Accounts"); err != nil {
		t.Fatalf("create purchase ledger: %v", err)
	}

	e.item = e.newItem(t, "Widget")
	e.batch = e.newBatch(t, e.item, "B-01")
	if err := pool.RunInTx(cctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT id FROM godown WHERE company_id = $1 AND name = 'Main Location'`,
			companyID).Scan(&e.godown)
	}); err != nil {
		t.Fatalf("the company seed must create a 'Main Location' for stock to sit in: %v", err)
	}
	return e
}

// newItem plants a stock item directly. masters.CreateStockItem still writes the
// columns 0025 renamed (it is slice INV-E's job to fix), so these tests must not
// depend on it.
func (e *stockEnv) newItem(t *testing.T, name string) uuid.UUID {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new stock item id: %v", err)
	}
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO stock_item (id, company_id, name) VALUES ($1, $2, $3)`,
			id, e.company, name+"-"+id.String()[:8])
		return err
	}); err != nil {
		t.Fatalf("create stock item %s: %v", name, err)
	}
	return id
}

func (e *stockEnv) newBatch(t *testing.T, item uuid.UUID, name string) uuid.UUID {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new batch id: %v", err)
	}
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO stock_batch (id, company_id, stock_item_id, name) VALUES ($1, $2, $3, $4)`,
			id, e.company, item, name)
		return err
	}); err != nil {
		t.Fatalf("create batch %s: %v", name, err)
	}
	return id
}

// purchaseOf builds a one-line purchase: Dr Purchases / Cr the creditor, with
// the supplied inventory lines. The accounting amount is the sum of the line
// amounts so the voucher reads like a real bill.
func (e *stockEnv) purchaseOf(lines ...InventoryLine) VoucherInput {
	total := decimal.Zero
	for _, l := range lines {
		total = total.Add(l.Amount)
	}
	if total.IsZero() {
		total = decimal.RequireFromString("1.0000")
	}
	return VoucherInput{
		Class:       Purchase,
		VoucherDate: "2025-05-10",
		Narration:   "Goods inward",
		Party:       "Acme Supplies",
		CreatedBy:   "tester",
		Postings: []PostingInput{
			{LedgerID: e.purchase, DrCr: Debit, Amount: total},
			{LedgerID: e.creditor, DrCr: Credit, Amount: total},
		},
		Inventory: lines,
	}
}

// inLine / outLine build a movement of the environment's item at Main Location.
func (e *stockEnv) inLine(qty, rate string) InventoryLine {
	return e.line(Inward, qty, rate)
}

func (e *stockEnv) outLine(qty, rate string) InventoryLine {
	return e.line(Outward, qty, rate)
}

func (e *stockEnv) line(d Direction, qty, rate string) InventoryLine {
	q := decimal.RequireFromString(qty)
	r := decimal.RequireFromString(rate)
	return InventoryLine{
		StockItemID: e.item,
		GodownID:    e.godown,
		Direction:   d,
		Qty:         q,
		Rate:        r,
		Amount:      q.Mul(r),
	}
}

// movementsOf returns the movement rows attached to one voucher VERSION.
func (e *stockEnv) movementsOf(t *testing.T, versionID uuid.UUID) []InventoryLineView {
	t.Helper()
	var out []InventoryLineView
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = loadInventoryLines(ctx, tx, e.company, versionID)
		return err
	}); err != nil {
		t.Fatalf("read movements of version %s: %v", versionID, err)
	}
	return out
}

// movementCount counts every movement row in the company, across all versions.
func (e *stockEnv) movementCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM stock_movement WHERE company_id = $1`, e.company).Scan(&n)
	}); err != nil {
		t.Fatalf("count movements: %v", err)
	}
	return n
}

// ---------------------------------------------------------------------------

// TestStockLinesSurviveTheStoredJSONBRoundTrip is the database half of the hash
// contract: the payload actually stored as JSONB must still re-derive to the
// bytes that were hashed, with the stock lines intact.
func TestStockLinesSurviveTheStoredJSONBRoundTrip(t *testing.T) {
	e := setupStock(t)

	in := e.purchaseOf(InventoryLine{
		StockItemID: e.item, GodownID: e.godown, BatchID: e.batch,
		Direction: Inward,
		Qty:       decimal.RequireFromString("12.3400"),
		Rate:      decimal.RequireFromString("250.0000"),
		Amount:    decimal.RequireFromString("3085.0000"),
	})
	res, err := e.led.PostVoucher(e.ctx, in)
	if err != nil {
		t.Fatalf("post purchase with stock: %v", err)
	}

	h, err := e.led.VoucherHistory(e.ctx, res.VoucherID)
	if err != nil {
		t.Fatalf("read history: %v", err)
	}
	if !h.Verification.OK {
		t.Fatalf("a freshly posted voucher carrying stock does not verify against its own hash: %s", h.Verification.Reason)
	}

	// The stored payload must carry the line, with the scale preserved.
	var stored string
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT payload::text FROM voucher_version WHERE company_id = $1 AND id = $2`,
			e.company, res.VoucherVersionID).Scan(&stored)
	}); err != nil {
		t.Fatalf("read stored payload: %v", err)
	}
	if !strings.Contains(stored, `"qty": "12.3400"`) && !strings.Contains(stored, `"qty":"12.3400"`) {
		t.Fatalf("the stored payload lost the stock quantity's fixed-4 string form, so the chain can no longer be re-derived from it: %s", stored)
	}
	if !strings.Contains(stored, e.batch.String()) {
		t.Fatalf("the stored payload does not name the batch the goods came from: %s", stored)
	}
}

// voucherCount counts the company's voucher heads.
func (e *stockEnv) voucherCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM voucher_head WHERE company_id = $1`, e.company).Scan(&n)
	}); err != nil {
		t.Fatalf("count vouchers: %v", err)
	}
	return n
}

// TestStockMovementRollsBackWithTheVoucher is the atomicity claim, tested from
// BOTH sides of the seam, because the two sides fail differently.
//
//	(a) THE ACCOUNTING FAILS. A post whose last posting names a ledger that does
//	    not exist must leave the warehouse exactly as it found it — including the
//	    movement rows that had already been inserted earlier in the same
//	    transaction.
//
//	(b) THE INVENTORY FAILS. This is the case that detects a movement write done
//	    in its OWN transaction after post() returns. Truly orphaned movement rows
//	    are structurally impossible — stock_movement's composite FK to
//	    voucher_version means a movement can never be committed before its
//	    version — so the observable symptom of a split transaction is the mirror
//	    image: the VOUCHER commits, the stock decision then refuses, and the books
//	    are left holding a purchase whose goods never arrived, with no error the
//	    user can act on because the voucher they were told failed is in fact
//	    saved.
func TestStockMovementRollsBackWithTheVoucher(t *testing.T) {
	e := setupStock(t)

	ghost, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new ghost ledger id: %v", err)
	}
	in := e.purchaseOf(e.inLine("10.0000", "100.0000"), e.inLine("5.0000", "100.0000"))
	// Point the CREDIT leg at a ledger that was never created. The amounts still
	// balance, so validation passes and the failure happens at the last INSERT —
	// after the movements have already been written in the same transaction.
	in.Postings[1].LedgerID = ghost

	if _, err := e.led.PostVoucher(e.ctx, in); err == nil {
		t.Fatal("a posting against a non-existent ledger must fail the whole post")
	}
	if n := e.movementCount(t); n != 0 {
		t.Fatalf("a failed post left %d stock movement(s) behind: the books rolled back and the warehouse did not, so stock now exists that no voucher accounts for", n)
	}
	if n := e.voucherCount(t); n != 0 {
		t.Fatalf("a failed post committed %d voucher(s)", n)
	}

	// (b) The accounting is impeccable; the stock is not.
	if _, err := e.led.PostVoucher(e.ctx, e.purchaseOf(e.outLine("10.0000", "100.0000"))); err == nil {
		t.Fatal("issuing stock the company does not have must fail the whole post")
	}
	if n := e.movementCount(t); n != 0 {
		t.Fatalf("a stock-refused post left %d movement(s) behind", n)
	}
	if n := e.voucherCount(t); n != 0 {
		t.Fatalf("a stock-refused post committed %d voucher(s): the inventory write does not share the voucher's transaction, so the user is told the entry failed while the books have already recorded it", n)
	}
}

// TestAlterRestatesMovementsAndDeleteErasesThem covers the versioning contract:
// an ALTER re-states the lines under a NEW version (the superseded ones stay in
// the log, which is what makes the edit auditable), and a DELETE tombstone
// carries none at all.
func TestAlterRestatesMovementsAndDeleteErasesThem(t *testing.T) {
	e := setupStock(t)

	v1, err := e.led.PostVoucher(e.ctx, e.purchaseOf(e.inLine("10.0000", "100.0000")))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if got := e.movementsOf(t, v1.VoucherVersionID); len(got) != 1 || !got[0].Qty.Equal(decimal.RequireFromString("10")) {
		t.Fatalf("version 1 should carry exactly one movement of 10, got %+v", got)
	}

	v2, err := e.led.AlterVoucher(e.ctx, v1.VoucherID, e.purchaseOf(e.inLine("4.0000", "100.0000")))
	if err != nil {
		t.Fatalf("alter: %v", err)
	}

	head := e.movementsOf(t, v2.VoucherVersionID)
	if len(head) != 1 {
		t.Fatalf("the altered version must carry exactly one movement, got %d — an alter that appends instead of restating double-counts the goods", len(head))
	}
	if !head[0].Qty.Equal(decimal.RequireFromString("4")) {
		t.Fatalf("the altered version records %s units where the correction said 4", head[0].Qty)
	}

	// The superseded line must still exist: it is the evidence of what the
	// voucher used to say.
	if old := e.movementsOf(t, v1.VoucherVersionID); len(old) != 1 || !old[0].Qty.Equal(decimal.RequireFromString("10")) {
		t.Fatalf("the superseded version's movement was destroyed by the alter (%+v): the edit log can no longer show what changed", old)
	}
	if n := e.movementCount(t); n != 2 {
		t.Fatalf("expected 2 movement rows to coexist after an alter (superseded + current), got %d", n)
	}

	del, err := e.led.DeleteVoucher(e.ctx, v1.VoucherID, "deleter")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if tomb := e.movementsOf(t, del.VoucherVersionID); len(tomb) != 0 {
		t.Fatalf("the DELETE tombstone carries %d movement(s): the deleted goods would sit on the shelf forever while the voucher had vanished from the books", len(tomb))
	}
}

// TestTombstoneRefusesStockLinesItIsHandedProves the guard itself, not merely
// that DeleteVoucher happens to pass none. post() is called directly with a
// tombstone that DOES carry inventory; it must record neither a movement row
// nor an inventory entry in the hashed payload.
func TestTombstoneRefusesStockLinesItIsHanded(t *testing.T) {
	e := setupStock(t)

	v1, err := e.led.PostVoucher(e.ctx, e.purchaseOf(e.inLine("10.0000", "100.0000")))
	if err != nil {
		t.Fatalf("post: %v", err)
	}

	tomb, err := e.led.post(e.ctx, v1.VoucherID, OpDelete, VoucherInput{
		Class:       Purchase,
		VoucherNo:   v1.VoucherNo,
		VoucherDate: "2025-05-10",
		CreatedBy:   "deleter",
		Inventory:   []InventoryLine{e.inLine("10.0000", "100.0000")},
	})
	if err != nil {
		t.Fatalf("tombstone post: %v", err)
	}

	if got := e.movementsOf(t, tomb.VoucherVersionID); len(got) != 0 {
		t.Fatalf("a tombstone handed %d stock line(s) wrote them: a deleted voucher would keep moving goods", len(got))
	}
	var payload string
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT payload::text FROM voucher_version WHERE company_id = $1 AND id = $2`,
			e.company, tomb.VoucherVersionID).Scan(&payload)
	}); err != nil {
		t.Fatalf("read tombstone payload: %v", err)
	}
	if strings.Contains(payload, `"inventory"`) {
		t.Fatalf("the tombstone's hashed payload still declares inventory, so the edit log claims the deletion moved stock: %s", payload)
	}
}

// TestFrozenVoucherIsRefusedBeforeAnyStockIsWritten. A voucher pinned by an IRN
// is hard-blocked; the refusal must be the *FrozenError (not some downstream
// failure) and the warehouse must be untouched.
func TestFrozenVoucherIsRefusedBeforeAnyStockIsWritten(t *testing.T) {
	e := setupStock(t)

	v1, err := e.led.PostVoucher(e.ctx, e.purchaseOf(e.inLine("10.0000", "100.0000")))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if _, err := e.led.RegisterExternalReference(e.ctx, v1.VoucherID, "IRN", "IRN-STOCK-1", "tester"); err != nil {
		t.Fatalf("register irn: %v", err)
	}
	before := e.movementCount(t)

	_, err = e.led.AlterVoucher(e.ctx, v1.VoucherID, e.purchaseOf(e.inLine("999.0000", "100.0000")))
	var fe *FrozenError
	if !errors.As(err, &fe) {
		t.Fatalf("altering an IRN-pinned voucher must return *FrozenError so the caller gets a 409 and a reconciliation row; got %v", err)
	}
	if fe.Ref != "IRN-STOCK-1" {
		t.Fatalf("the refusal names reference %q, not the IRN that blocked it", fe.Ref)
	}

	if after := e.movementCount(t); after != before {
		t.Fatalf("a frozen voucher's refused alter changed the movement count from %d to %d: stock was written for an edit that was rejected", before, after)
	}
	if got := e.movementsOf(t, v1.VoucherVersionID); len(got) != 1 || !got[0].Qty.Equal(decimal.RequireFromString("10")) {
		t.Fatalf("the frozen voucher's own movements were disturbed by the refused alter: %+v", got)
	}

	// THE FREEZE TAKES PRECEDENCE OVER EVERY INVENTORY RULE. This alter is also
	// stock-negative, so whichever check runs first decides the error the caller
	// sees. It must be the freeze: only *FrozenError produces the 409 and the
	// reconciliation_queue row that gets a filed invoice reconciled. A
	// NegativeStockError here would be a data-entry complaint about a voucher the
	// user is not permitted to edit at all, and the recon queue would stay empty.
	_, err = e.led.AlterVoucher(e.ctx, v1.VoucherID, e.purchaseOf(e.outLine("500.0000", "100.0000")))
	if !errors.As(err, &fe) {
		t.Fatalf("a frozen voucher must be refused as frozen even when the edit is also stock-negative, so the refusal reaches the reconciliation queue; got %v", err)
	}
}

// TestNegativeStockIsRefusedWithANamedError. The deferred trigger is the
// authority; this proves the Go pre-check turns its refusal into a message a
// data-entry operator can act on — which item, which location, what balance —
// and that nothing commits.
func TestNegativeStockIsRefusedWithANamedError(t *testing.T) {
	e := setupStock(t)

	var itemName, godownName string
	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT name FROM stock_item WHERE company_id = $1 AND id = $2`,
			e.company, e.item).Scan(&itemName); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT name FROM godown WHERE company_id = $1 AND id = $2`,
			e.company, e.godown).Scan(&godownName)
	}); err != nil {
		t.Fatalf("read master names: %v", err)
	}

	in := e.purchaseOf(e.outLine("10.0000", "100.0000"))
	_, err := e.led.PostVoucher(e.ctx, in)

	var ne *NegativeStockError
	if !errors.As(err, &ne) {
		t.Fatalf("issuing 10 units of an item that has none must be refused as *NegativeStockError; got %v", err)
	}
	if ne.ItemName != itemName {
		t.Fatalf("the refusal names item %q, but the operator entered %q — a message that does not name the item cannot be acted on", ne.ItemName, itemName)
	}
	if ne.GodownName != godownName {
		t.Fatalf("the refusal names location %q, not %q: with multiple locations the operator cannot tell where the stock is missing", ne.GodownName, godownName)
	}
	if !ne.Closing.Equal(decimal.RequireFromString("-10")) {
		t.Fatalf("the refusal reports a closing balance of %s, not -10: the operator is not told how short they are", ne.Closing)
	}
	msg := ne.Error()
	for _, want := range []string{itemName, godownName, "-10.0000"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("the refusal message %q does not mention %q", msg, want)
		}
	}

	if n := e.movementCount(t); n != 0 {
		t.Fatalf("a refused negative-stock post committed %d movement(s)", n)
	}
	if n := e.voucherCount(t); n != 0 {
		t.Fatalf("a refused negative-stock post still committed %d voucher(s)", n)
	}
}

// TestNegativeStockOptInAllowsTheIssue proves the refusal is a company SETTING,
// not a hard rule: trades that sell before the supplier's invoice arrives turn
// it on and the same post succeeds.
func TestNegativeStockOptInAllowsTheIssue(t *testing.T) {
	e := setupStock(t)

	if err := e.pool.RunInTx(e.ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE company SET allow_negative_stock = true WHERE id = $1`, e.company)
		return err
	}); err != nil {
		t.Fatalf("enable negative stock: %v", err)
	}

	res, err := e.led.PostVoucher(e.ctx, e.purchaseOf(e.outLine("10.0000", "100.0000")))
	if err != nil {
		t.Fatalf("with allow_negative_stock on, the issue must be accepted: %v", err)
	}
	if got := e.movementsOf(t, res.VoucherVersionID); len(got) != 1 {
		t.Fatalf("expected the outward movement to be recorded, got %d", len(got))
	}
}

// TestAlterMayRaiseAnOutwardToTheWholeBalance is the pre-check's hardest case.
// At the moment the check runs, voucher_head still points at the version being
// SUPERSEDED, so a check that forgot to exclude this voucher would compare the
// new outward against a balance that still contains the old one — and refuse a
// perfectly legal correction. The deferred trigger does not have this problem
// (it fires after the head has moved), so a wrong pre-check would be a refusal
// the database itself would have allowed.
func TestAlterMayRaiseAnOutwardToTheWholeBalance(t *testing.T) {
	e := setupStock(t)

	if _, err := e.led.PostVoucher(e.ctx, e.purchaseOf(e.inLine("100.0000", "10.0000"))); err != nil {
		t.Fatalf("inward: %v", err)
	}
	issue, err := e.led.PostVoucher(e.ctx, e.purchaseOf(e.outLine("10.0000", "10.0000")))
	if err != nil {
		t.Fatalf("issue 10: %v", err)
	}

	// Correct the issue from 10 to the full 100. Legal: the final position is 0.
	if _, err := e.led.AlterVoucher(e.ctx, issue.VoucherID, e.purchaseOf(e.outLine("100.0000", "10.0000"))); err != nil {
		t.Fatalf("raising an outward from 10 to the full balance of 100 must be allowed (the final position is zero): %v", err)
	}

	// And one unit beyond the balance must still be refused.
	if _, err := e.led.AlterVoucher(e.ctx, issue.VoucherID, e.purchaseOf(e.outLine("101.0000", "10.0000"))); err == nil {
		t.Fatal("issuing 101 units against a balance of 100 must be refused")
	}
}

// TestGetVoucherReturnsInventoryLines. The projection must return REAL uuids,
// not names: the alter path re-posts what it fetched.
func TestGetVoucherReturnsInventoryLines(t *testing.T) {
	e := setupStock(t)

	first := InventoryLine{
		StockItemID: e.item, GodownID: e.godown, BatchID: e.batch,
		Direction:   Inward,
		Qty:         decimal.RequireFromString("12.5000"),
		Rate:        decimal.RequireFromString("40.0000"),
		DiscountPct: decimal.RequireFromString("2.5000"),
		Amount:      decimal.RequireFromString("487.5000"),
	}
	// A second line with NO dimensions at all: an untracked position must come
	// back as uuid.Nil, not as a phantom all-zeroes master.
	second := InventoryLine{
		StockItemID: e.item,
		Direction:   Inward,
		Qty:         decimal.RequireFromString("1.0000"),
		Rate:        decimal.RequireFromString("40.0000"),
		Amount:      decimal.RequireFromString("40.0000"),
	}
	res, err := e.led.PostVoucher(e.ctx, e.purchaseOf(first, second))
	if err != nil {
		t.Fatalf("post: %v", err)
	}

	v, err := e.led.GetVoucher(e.ctx, res.VoucherID)
	if err != nil {
		t.Fatalf("get voucher: %v", err)
	}
	if len(v.InventoryLines) != 2 {
		t.Fatalf("GetVoucher returned %d inventory line(s) for a two-line voucher: an alter re-posting this view would silently write the stock off", len(v.InventoryLines))
	}

	got := v.InventoryLines[0]
	if got.LineNo != 1 {
		t.Fatalf("lines must come back in keyed order; first line is line_no %d", got.LineNo)
	}
	if got.StockItemID != e.item {
		t.Fatalf("line 1 names item %s, not the item that was posted (%s) — a name-only projection cannot be re-posted", got.StockItemID, e.item)
	}
	if got.GodownID != e.godown || got.BatchID != e.batch {
		t.Fatalf("line 1 lost its dimensions: godown %s (want %s), batch %s (want %s)",
			got.GodownID, e.godown, got.BatchID, e.batch)
	}
	if got.ItemName == "" || got.GodownName == "" || got.BatchName != "B-01" {
		t.Fatalf("line 1 must resolve its master names for display: item %q godown %q batch %q",
			got.ItemName, got.GodownName, got.BatchName)
	}
	if got.Direction != string(Inward) {
		t.Fatalf("line 1 direction is %q, want IN", got.Direction)
	}
	if !got.Qty.Equal(decimal.RequireFromString("12.5")) ||
		!got.Rate.Equal(decimal.RequireFromString("40")) ||
		!got.DiscountPct.Equal(decimal.RequireFromString("2.5")) ||
		!got.Amount.Equal(decimal.RequireFromString("487.5")) {
		t.Fatalf("line 1 numbers came back as qty %s rate %s disc %s amount %s",
			got.Qty, got.Rate, got.DiscountPct, got.Amount)
	}

	bare := v.InventoryLines[1]
	if bare.GodownID != uuid.Nil || bare.BatchID != uuid.Nil {
		t.Fatalf("an untracked dimension must project as the nil uuid, got godown %s batch %s", bare.GodownID, bare.BatchID)
	}
	if bare.GodownName != "" || bare.BatchName != "" {
		t.Fatalf("an untracked dimension must have no name, got godown %q batch %q", bare.GodownName, bare.BatchName)
	}
}

// TestAlterRoundTripPreservesStock is the whole point of projecting real ids:
// fetch a voucher, rebuild the input from what was fetched (exactly as the F9
// screen does when it re-posts an edited form), alter, and the movements must be
// identical. If GetVoucher dropped the lines, this alter would quietly write the
// entire inventory off while looking like a successful edit.
func TestAlterRoundTripPreservesStock(t *testing.T) {
	e := setupStock(t)

	orig := InventoryLine{
		StockItemID: e.item, GodownID: e.godown, BatchID: e.batch,
		Direction:   Inward,
		Qty:         decimal.RequireFromString("7.0000"),
		Rate:        decimal.RequireFromString("15.0000"),
		DiscountPct: decimal.RequireFromString("1.0000"),
		Amount:      decimal.RequireFromString("103.9500"),
	}
	res, err := e.led.PostVoucher(e.ctx, e.purchaseOf(orig))
	if err != nil {
		t.Fatalf("post: %v", err)
	}

	v, err := e.led.GetVoucher(e.ctx, res.VoucherID)
	if err != nil {
		t.Fatalf("get voucher: %v", err)
	}

	// Rebuild the input the way a caller would: only from the fetched view.
	rebuilt := VoucherInput{
		Class:       Class(v.Class),
		VoucherNo:   v.VoucherNo,
		VoucherDate: v.VoucherDate,
		Narration:   "Goods inward (corrected narration)",
		Party:       v.Party,
		CreatedBy:   "editor",
	}
	for _, p := range v.Postings {
		rebuilt.Postings = append(rebuilt.Postings, PostingInput{
			LedgerID: p.LedgerID, DrCr: DrCr(p.DrCr), Amount: p.Amount,
		})
	}
	for _, l := range v.InventoryLines {
		rebuilt.Inventory = append(rebuilt.Inventory, InventoryLine{
			StockItemID: l.StockItemID,
			GodownID:    l.GodownID,
			BatchID:     l.BatchID,
			Direction:   Direction(l.Direction),
			Qty:         l.Qty,
			Rate:        l.Rate,
			DiscountPct: l.DiscountPct,
			Amount:      l.Amount,
		})
	}

	alt, err := e.led.AlterVoucher(e.ctx, res.VoucherID, rebuilt)
	if err != nil {
		t.Fatalf("alter from the fetched view: %v", err)
	}

	after := e.movementsOf(t, alt.VoucherVersionID)
	if len(after) != 1 {
		t.Fatalf("a narration-only edit left %d stock line(s) where 1 was posted: the goods were written off by an edit that never mentioned them", len(after))
	}
	a := after[0]
	if a.StockItemID != orig.StockItemID || a.GodownID != orig.GodownID || a.BatchID != orig.BatchID {
		t.Fatalf("the round trip moved the line to a different position: item %s godown %s batch %s", a.StockItemID, a.GodownID, a.BatchID)
	}
	if !a.Qty.Equal(orig.Qty) || !a.Rate.Equal(orig.Rate) ||
		!a.DiscountPct.Equal(orig.DiscountPct) || !a.Amount.Equal(orig.Amount) {
		t.Fatalf("the round trip changed the numbers: qty %s rate %s disc %s amount %s",
			a.Qty, a.Rate, a.DiscountPct, a.Amount)
	}
	if a.Direction != string(orig.Direction) {
		t.Fatalf("the round trip flipped the direction to %s", a.Direction)
	}

	// And the chain still verifies over both versions.
	h, err := e.led.VoucherHistory(e.ctx, res.VoucherID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if !h.Verification.OK {
		t.Fatalf("the chain broke across an inventory alter: %s", h.Verification.Reason)
	}
}

// TestInventoryValidationRefusesAMalformedLine keeps the friendly early check
// honest: a signed quantity is the mistake that matters, because `direction`
// already carries the sign and an OUT of -5 would be an inward wearing the wrong
// label.
func TestInventoryValidationRefusesAMalformedLine(t *testing.T) {
	item, _ := uuid.NewV7()
	cases := []struct {
		name string
		line InventoryLine
		want string
	}{
		{"no item", InventoryLine{Direction: Inward, Qty: decimal.NewFromInt(1)}, "stock item id is required"},
		{"bad direction", InventoryLine{StockItemID: item, Direction: "SIDEWAYS", Qty: decimal.NewFromInt(1)}, "invalid direction"},
		{"zero qty", InventoryLine{StockItemID: item, Direction: Inward, Qty: decimal.Zero}, "quantity must be positive"},
		{"signed qty", InventoryLine{StockItemID: item, Direction: Outward, Qty: decimal.NewFromInt(-5)}, "quantity must be positive"},
		{"negative rate", InventoryLine{StockItemID: item, Direction: Inward, Qty: decimal.NewFromInt(1), Rate: decimal.NewFromInt(-1)}, "rate must be non-negative"},
		{"discount over 100", InventoryLine{StockItemID: item, Direction: Inward, Qty: decimal.NewFromInt(1), DiscountPct: decimal.NewFromInt(101)}, "discount must be between 0 and 100"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateInventory([]InventoryLine{tc.line})
			if err == nil {
				t.Fatalf("a %s line was accepted; it would reach the database as a CHECK violation the operator cannot read", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("refusal for a %s line reads %q, which does not explain %q", tc.name, err, tc.want)
			}
		})
	}
}
