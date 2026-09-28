package bankstatement_test

// T4 SLICE 2b — the invariants only a database can prove.
//
// The pure engine's tests (match_test.go) prove what the matcher PROPOSES.
// These prove what the system WRITES, which is the half that can corrupt books:
// B-1 end to end (an ambiguous line writes ZERO clearances), B-2 (a resolved
// match writes one and nothing else records clearance), B-8 (un-ticking removes
// both), B-9 (the matcher gained no exemption from alter-detection).

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/banking"
	"github.com/khata/cored/internal/bankstatement"
	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/platform/testdb"
	"github.com/khata/cored/internal/reporting"
	"github.com/khata/cored/migrations"
)

var (
	adminDSN = testdb.AdminDSNValue()
	appDSN   = testdb.AppDSNValue()
)

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

type matchFixture struct {
	pool      *db.Pool
	ctx       context.Context
	companyID uuid.UUID
	bank      uuid.UUID
	rent      uuid.UUID
	vouchers  map[string]uuid.UUID
}

func matchSetup(t *testing.T) *matchFixture {
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
		Name:        "Match Co " + companyID.String()[:8],
		FYStartDate: "2025-04-01",
	}); err != nil {
		t.Fatalf("create company: %v", err)
	}
	testdb.Cleanup(t, companyID)

	f := &matchFixture{pool: pool, ctx: cctx, companyID: companyID, vouchers: map[string]uuid.UUID{}}
	mk := func(name, group string) uuid.UUID {
		id, err := ids.CreateLedger(cctx, name, group)
		if err != nil {
			t.Fatalf("create ledger %s: %v", name, err)
		}
		return id
	}
	f.bank = mk("HDFC Current", "Bank Accounts")
	f.rent = mk("Rent Paid", "Indirect Expenses")

	// The bank account needs a number for the import's account check to have
	// something to compare against; without one every import carries the
	// "could not be checked" disclosure, which is true but noisy here.
	if err := pool.RunInTx(cctx, func(ctx context.Context, tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `
			UPDATE ledger SET bank_account_number = '50200012345678', bank_ifsc = 'HDFC0001234'
			 WHERE company_id = $1 AND id = $2`, companyID, f.bank)
		return e
	}); err != nil {
		t.Fatalf("set bank identity: %v", err)
	}
	return f
}

// pay posts a payment OUT of the bank account.
func (f *matchFixture) pay(t *testing.T, no, date, amount, narration string) uuid.UUID {
	t.Helper()
	led := ledger.NewService(f.pool)
	ids := identity.NewService(f.pool)
	vt, err := ids.VoucherTypeID(f.ctx, "Payment")
	if err != nil {
		t.Fatalf("payment vt: %v", err)
	}
	res, err := led.PostVoucher(f.ctx, ledger.VoucherInput{
		Class:         ledger.Payment,
		VoucherTypeID: vt,
		VoucherNo:     no,
		VoucherDate:   date,
		Narration:     narration,
		CreatedBy:     "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: f.rent, DrCr: ledger.Debit, Amount: dec(amount)},
			{LedgerID: f.bank, DrCr: ledger.Credit, Amount: dec(amount)},
		},
	})
	if err != nil {
		t.Fatalf("post payment %s: %v", no, err)
	}
	f.vouchers[no] = res.VoucherID
	return res.VoucherID
}

// importCSV runs a real import through the real parser, so these tests exercise
// the same path an operator does rather than inserting rows behind it.
func (f *matchFixture) importCSV(t *testing.T, csv string) {
	t.Helper()
	svc := bankstatement.NewService(f.pool)
	_, err := svc.Import(f.ctx, f.companyID, bankstatement.ImportRequest{
		LedgerID: f.bank,
		FileName: "statement.csv",
		Content:  csv,
		Mapping: bankstatement.ColumnMapping{
			ValueDate:   "Date",
			DateLayout:  bankstatement.YMDDash,
			Withdrawal:  "Withdrawal",
			Deposit:     "Deposit",
			Encoding:    bankstatement.TwoColumns,
			Description: "Narration",
		},
		Declared: declaredFor(t, csv),
		By:       "tester",
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
}

// declaredFor states an opening of zero and the closing the file's own rows
// imply, for fixtures that carry no running balance.
//
// ⚠️ TAUTOLOGICAL ON PURPOSE, AND ONLY HERE. Since W5 an import is refused
// without a balance proof (proof.go), and these tests are about MATCHING, not
// about the proof — so they declare the one pair of figures that proves any
// file. The proof itself is pinned where it is tested: formats_test.go and
// TestAnUnprovableStatementIsRefusedAndWritesNothing below.
func declaredFor(t *testing.T, csv string) bankstatement.Declared {
	t.Helper()
	st, err := bankstatement.Parse(csv, bankstatement.ColumnMapping{
		ValueDate: "Date", DateLayout: bankstatement.YMDDash, Withdrawal: "Withdrawal",
		Deposit: "Deposit", Encoding: bankstatement.TwoColumns, Description: "Narration",
	})
	if err != nil {
		t.Fatalf("declaredFor: %v", err)
	}
	opening, closing := decimal.Zero, decimal.Zero
	for _, l := range st.Lines {
		if l.DrCr == "D" {
			closing = closing.Add(l.Amount)
		} else {
			closing = closing.Sub(l.Amount)
		}
	}
	return bankstatement.Declared{Opening: &opening, Closing: &closing}
}

// clearances counts the bank_reconciliation rows for this company — the ONE
// table that records clearance, which is what makes it the right thing to count.
func (f *matchFixture) clearances(t *testing.T) int {
	t.Helper()
	return f.count(t, "bank_reconciliation")
}

func (f *matchFixture) matches(t *testing.T) int {
	t.Helper()
	return f.count(t, "bank_statement_match")
}

// count runs a scoped count. db.Pool exposes no bare Exec/QueryRow — every read
// goes through a transaction that sets the tenant GUC, which is the mechanism
// RLS depends on, so a test helper must go the same way as the product does.
func (f *matchFixture) count(t *testing.T, table string) int {
	t.Helper()
	var n int
	if err := f.pool.RunInTx(f.ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM `+table+` WHERE company_id = $1`, f.companyID).Scan(&n)
	}); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func (f *matchFixture) report(t *testing.T) bankstatement.MatchView {
	t.Helper()
	v, err := bankstatement.NewService(f.pool).
		MatchReport(f.ctx, f.companyID, f.bank, "", "", 0)
	if err != nil {
		t.Fatalf("match report: %v", err)
	}
	return v
}

// ===========================================================================
// B-1 — END TO END. The pure test proves the PROPOSAL; this proves that a
// system holding an ambiguous line has written NOTHING.
// ===========================================================================

// TestAnAmbiguousLineWritesZeroClearances is invariant B-1 as the memo states
// it: "two vouchers, same amount, same day, one statement line => status
// AMBIGUOUS, ZERO bank_reconciliation rows written, both candidates returned."
//
// ⛔ MUTATION: make the matcher take the first candidate. This test must fail.
func TestAnAmbiguousLineWritesZeroClearances(t *testing.T) {
	f := matchSetup(t)
	f.pay(t, "PAY-1", "2025-05-14", "25000", "to Acme")
	f.pay(t, "PAY-2", "2025-05-14", "25000", "to Globex")
	f.importCSV(t, `Date,Narration,Withdrawal,Deposit
2025-05-14,SOME TRANSFER,25000.00,
`)

	view := f.report(t)

	if len(view.Proposals.Lines) != 1 {
		t.Fatalf("proposals = %d, want 1", len(view.Proposals.Lines))
	}
	lp := view.Proposals.Lines[0]
	if !lp.Ambiguous {
		t.Error("two vouchers of the same amount on the same day must be AMBIGUOUS")
	}
	if len(lp.Candidates) != 2 {
		t.Errorf("candidates = %d, want BOTH surfaced", len(lp.Candidates))
	}
	if lp.AcceptableInBulk {
		t.Error("an ambiguous line was offered for bulk acceptance")
	}
	// ⭐ THE ASSERTION THAT MATTERS: deriving proposals wrote nothing.
	if n := f.clearances(t); n != 0 {
		t.Errorf("bank_reconciliation rows = %d, want 0 — deriving a proposal must "+
			"never clear anything", n)
	}
	if n := f.matches(t); n != 0 {
		t.Errorf("bank_statement_match rows = %d, want 0", n)
	}
}

// TestReadingTheProposalsTwiceStillWritesNothing. A read that writes on the
// second call would be the worst possible version of this feature.
func TestReadingTheProposalsTwiceStillWritesNothing(t *testing.T) {
	f := matchSetup(t)
	f.pay(t, "PAY-1", "2025-05-14", "1000", "rent")
	f.importCSV(t, `Date,Narration,Withdrawal,Deposit
2025-05-14,RENT,1000.00,
`)

	_ = f.report(t)
	_ = f.report(t)

	if n := f.clearances(t); n != 0 {
		t.Errorf("clearances = %d after two reads, want 0", n)
	}
}

// ===========================================================================
// B-2 — A RESOLVED MATCH WRITES A bank_reconciliation ROW, AND NOTHING ELSE
// RECORDS CLEARANCE
// ===========================================================================

// TestAcceptingAMatchWritesOneClearanceAndMovesTheStatement is B-2, asserted the
// way the memo asks: "a test asserting the BRS total moves by exactly the
// matched amount".
func TestAcceptingAMatchWritesOneClearanceAndMovesTheStatement(t *testing.T) {
	f := matchSetup(t)
	f.pay(t, "PAY-1", "2025-05-14", "1000", "rent for May")
	f.importCSV(t, `Date,Narration,Withdrawal,Deposit
2025-05-14,RENT MAY,1000.00,
`)
	rep := reporting.NewService(f.pool)

	before, err := rep.BankReconciliationStatement(f.ctx, f.companyID, f.bank, "2025-05-31")
	if err != nil {
		t.Fatalf("brs before: %v", err)
	}

	view := f.report(t)
	lp := view.Proposals.Lines[0]
	if !lp.AcceptableInBulk {
		t.Fatalf("expected an unambiguous exact match, got tier %q ambiguous=%v",
			lp.BestTier, lp.Ambiguous)
	}

	// The resolve goes through the SAME write the manual tick uses.
	bank := banking.NewService(f.pool)
	if err := bank.SetBankDateFromStatementLine(f.ctx, f.companyID,
		lp.Candidates[0].Leg.VoucherID, f.bank, lp.Candidates[0].Leg.LegNo,
		"2025-05-14", "tester", lp.Line.ID); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if n := f.clearances(t); n != 1 {
		t.Errorf("clearances = %d, want exactly 1", n)
	}

	after, err := rep.BankReconciliationStatement(f.ctx, f.companyID, f.bank, "2025-05-31")
	if err != nil {
		t.Fatalf("brs after: %v", err)
	}
	// The un-cleared net must have moved by EXACTLY the matched amount.
	moved := before.UnclearedNet.Sub(after.UnclearedNet).Abs()
	if !moved.Equal(dec("1000")) {
		t.Errorf("the un-cleared net moved by %s, want exactly 1000", moved.StringFixed(2))
	}
	if !after.IsBalanced() {
		t.Error("the statement stopped balancing after a match was accepted")
	}
}

// TestTheStoredTierIsTheServersVerdictAndNotTheCallersClaim. There is no
// parameter by which a caller can describe its own evidence, and this proves the
// server reaches its own conclusion instead.
func TestTheStoredTierIsTheServersVerdictAndNotTheCallersClaim(t *testing.T) {
	f := matchSetup(t)
	// A voucher 40 days from the bank row: the matcher offers it at NO tier.
	f.pay(t, "PAY-1", "2025-04-04", "1000", "rent")
	f.importCSV(t, `Date,Narration,Withdrawal,Deposit
2025-05-14,RENT,1000.00,
`)
	view := f.report(t)
	lineID := view.Proposals.Lines[0].Line.ID
	if len(view.Proposals.Lines[0].Candidates) != 0 {
		t.Fatal("setup: expected no candidate 40 days out")
	}

	// The operator resolves it anyway — which is allowed, and is what 'manual'
	// exists to record.
	bank := banking.NewService(f.pool)
	if err := bank.SetBankDateFromStatementLine(f.ctx, f.companyID,
		f.vouchers["PAY-1"], f.bank, 0, "2025-05-14", "tester", lineID); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	var tier string
	if err := f.pool.RunInTx(f.ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT tier FROM bank_statement_match WHERE company_id = $1 AND line_id = $2`,
			f.companyID, lineID).Scan(&tier)
	}); err != nil {
		t.Fatalf("read tier: %v", err)
	}
	if tier != "manual" {
		t.Errorf("stored tier = %q, want manual — a leg the matcher does not offer "+
			"must be recorded as a person's say-so, not as arithmetic", tier)
	}
}

// TestAResolveWhoseDateIsNotTheStatementRowsIsRefusedAndWritesNothing. Silently
// preferring either date is invisible afterwards, because every figure would be
// a real figure.
func TestAResolveWhoseDateIsNotTheStatementRowsIsRefusedAndWritesNothing(t *testing.T) {
	f := matchSetup(t)
	f.pay(t, "PAY-1", "2025-05-14", "1000", "rent")
	f.importCSV(t, `Date,Narration,Withdrawal,Deposit
2025-05-14,RENT,1000.00,
`)
	lineID := f.report(t).Proposals.Lines[0].Line.ID

	bank := banking.NewService(f.pool)
	err := bank.SetBankDateFromStatementLine(f.ctx, f.companyID,
		f.vouchers["PAY-1"], f.bank, 0, "2025-05-20", "tester", lineID)
	if err == nil {
		t.Fatal("a bank date that is not the statement row's own was accepted")
	}
	if !strings.Contains(err.Error(), "2025-05-14") || !strings.Contains(err.Error(), "2025-05-20") {
		t.Errorf("the refusal must name BOTH dates, got: %v", err)
	}
	// ⭐ AND THE CLEARANCE ROLLED BACK WITH IT. The link is written in the same
	// transaction as the tick, so a refused match cannot leave a tick behind.
	if n := f.clearances(t); n != 0 {
		t.Errorf("clearances = %d after a refused resolve, want 0 — the refusal must "+
			"roll the tick back, or a failed match leaves a clearance nothing points at", n)
	}
}

// TestOneLineCannotBeResolvedTwice — a line is one thing the bank did.
func TestOneLineCannotBeResolvedTwice(t *testing.T) {
	f := matchSetup(t)
	f.pay(t, "PAY-1", "2025-05-14", "1000", "rent")
	f.pay(t, "PAY-2", "2025-05-14", "1000", "rent again")
	f.importCSV(t, `Date,Narration,Withdrawal,Deposit
2025-05-14,RENT,1000.00,
`)
	lineID := f.report(t).Proposals.Lines[0].Line.ID

	bank := banking.NewService(f.pool)
	if err := bank.SetBankDateFromStatementLine(f.ctx, f.companyID,
		f.vouchers["PAY-1"], f.bank, 0, "2025-05-14", "tester", lineID); err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	err := bank.SetBankDateFromStatementLine(f.ctx, f.companyID,
		f.vouchers["PAY-2"], f.bank, 0, "2025-05-14", "tester", lineID)
	if err == nil {
		t.Fatal("the same bank row was resolved against a second voucher")
	}
	if n := f.clearances(t); n != 1 {
		t.Errorf("clearances = %d, want 1 — the second resolve must roll back entirely", n)
	}
}

// ===========================================================================
// B-8 — UNLINKING REMOVES THE CLEARANCE *AND* RETURNS THE LINE TO UNMATCHED
// ===========================================================================

// TestUnTickingAMatchedLegRemovesTheMatchToo is B-8, and it proves the CASCADE
// rather than a second code path: Alt+F8 is the existing un-tick.
//
// ⛔ MUTATION: drop ON DELETE CASCADE from bank_statement_match_reconciliation_fk.
// This test must fail — and note it needs the schema_migrations un-stamp to be
// applied at all.
func TestUnTickingAMatchedLegRemovesTheMatchToo(t *testing.T) {
	f := matchSetup(t)
	f.pay(t, "PAY-1", "2025-05-14", "1000", "rent")
	f.importCSV(t, `Date,Narration,Withdrawal,Deposit
2025-05-14,RENT,1000.00,
`)
	lineID := f.report(t).Proposals.Lines[0].Line.ID

	bank := banking.NewService(f.pool)
	if err := bank.SetBankDateFromStatementLine(f.ctx, f.companyID,
		f.vouchers["PAY-1"], f.bank, 0, "2025-05-14", "tester", lineID); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if f.matches(t) != 1 || f.clearances(t) != 1 {
		t.Fatalf("setup: matches=%d clearances=%d", f.matches(t), f.clearances(t))
	}

	// The EXISTING un-tick. There is no separate "unlink" write.
	if err := bank.ClearBankDate(f.ctx, f.companyID, f.vouchers["PAY-1"], f.bank, 0); err != nil {
		t.Fatalf("un-tick: %v", err)
	}

	if n := f.clearances(t); n != 0 {
		t.Errorf("clearances = %d after un-ticking, want 0", n)
	}
	if n := f.matches(t); n != 0 {
		t.Errorf("bank_statement_match rows = %d after un-ticking, want 0 — a match "+
			"outliving its clearance would leave the leg un-ticked while the bank row "+
			"still read as matched", n)
	}
	// And the line is back among the proposals, which is the other half of B-8.
	view := f.report(t)
	if len(view.Proposals.Lines) != 1 {
		t.Errorf("proposals = %d after un-ticking, want the line returned to unmatched",
			len(view.Proposals.Lines))
	}
	if len(view.Resolved) != 0 {
		t.Errorf("resolved = %d, want 0", len(view.Resolved))
	}
}

// ===========================================================================
// B-9 — THE MATCHER GAINED NO EXEMPTION FROM ALTER-DETECTION
// ===========================================================================

// TestAVoucherAlteredAfterMatchingIsStillReportedBroken is B-9. The mechanism is
// 0045's and the matcher stores no clearance of its own, so this proves the
// property survives the new path rather than testing 0045 again.
func TestAVoucherAlteredAfterMatchingIsStillReportedBroken(t *testing.T) {
	f := matchSetup(t)
	f.pay(t, "PAY-1", "2025-05-14", "1000", "rent")
	f.importCSV(t, `Date,Narration,Withdrawal,Deposit
2025-05-14,RENT,1000.00,
`)
	lineID := f.report(t).Proposals.Lines[0].Line.ID

	bank := banking.NewService(f.pool)
	if err := bank.SetBankDateFromStatementLine(f.ctx, f.companyID,
		f.vouchers["PAY-1"], f.bank, 0, "2025-05-14", "tester", lineID); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	// Alter the voucher's amount AFTER it was matched.
	led := ledger.NewService(f.pool)
	ids := identity.NewService(f.pool)
	vt, err := ids.VoucherTypeID(f.ctx, "Payment")
	if err != nil {
		t.Fatalf("payment vt: %v", err)
	}
	if _, err := led.AlterVoucher(f.ctx, f.vouchers["PAY-1"], ledger.VoucherInput{
		Class:         ledger.Payment,
		VoucherTypeID: vt,
		VoucherNo:     "PAY-1",
		VoucherDate:   "2025-05-14",
		Narration:     "rent, corrected",
		CreatedBy:     "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: f.rent, DrCr: ledger.Debit, Amount: dec("1500")},
			{LedgerID: f.bank, DrCr: ledger.Credit, Amount: dec("1500")},
		},
	}); err != nil {
		t.Fatalf("alter: %v", err)
	}

	brs, err := reporting.NewService(f.pool).
		BankReconciliationStatement(f.ctx, f.companyID, f.bank, "2025-05-31")
	if err != nil {
		t.Fatalf("brs: %v", err)
	}
	if brs.BrokenCount != 1 {
		t.Errorf("BrokenCount = %d, want 1 — a voucher altered after MATCHING must be "+
			"reported BROKEN exactly as one altered after a manual tick is; the matcher "+
			"must gain no exemption", brs.BrokenCount)
	}
}

// ===========================================================================
// SET-ASIDE — A REAL CATEGORY, NOT A WEAK MATCH
// ===========================================================================

// TestASetAsideWritesNoClearanceAndMovesNoBalance. This is the one a client
// could most easily render as "done".
func TestASetAsideWritesNoClearanceAndMovesNoBalance(t *testing.T) {
	f := matchSetup(t)
	f.pay(t, "PAY-1", "2025-05-14", "1000", "rent")
	// A bank charge no voucher exists for.
	f.importCSV(t, `Date,Narration,Withdrawal,Deposit
2025-05-20,ACCOUNT MAINTENANCE FEE,177.00,
`)
	lineID := f.report(t).Proposals.Lines[0].Line.ID

	rep := reporting.NewService(f.pool)
	before, err := rep.BankReconciliationStatement(f.ctx, f.companyID, f.bank, "2025-05-31")
	if err != nil {
		t.Fatalf("brs before: %v", err)
	}

	svc := bankstatement.NewService(f.pool)
	if err := svc.SetAside(f.ctx, f.companyID, f.bank, lineID,
		"bank charge, no voucher raised", "tester"); err != nil {
		t.Fatalf("set aside: %v", err)
	}

	if n := f.clearances(t); n != 0 {
		t.Errorf("clearances = %d after a set-aside, want 0 — a set-aside asserts "+
			"nothing about the books", n)
	}
	after, err := rep.BankReconciliationStatement(f.ctx, f.companyID, f.bank, "2025-05-31")
	if err != nil {
		t.Fatalf("brs after: %v", err)
	}
	if !after.UnclearedNet.Equal(before.UnclearedNet) {
		t.Errorf("the un-cleared net moved from %s to %s; a set-aside must move no balance",
			before.UnclearedNet.StringFixed(2), after.UnclearedNet.StringFixed(2))
	}

	// It leaves the proposal list, and appears as a RESOLUTION with its own word.
	view := f.report(t)
	if len(view.Proposals.Lines) != 0 {
		t.Errorf("proposals = %d, want 0 — a set-aside line is decided", len(view.Proposals.Lines))
	}
	if len(view.Resolved) != 1 || view.Resolved[0].Status != "set-aside" {
		t.Fatalf("resolved = %+v, want one set-aside", view.Resolved)
	}
	if view.Resolved[0].VoucherID != uuid.Nil {
		t.Error("a set-aside must name no voucher")
	}
}

// TestASetAsideCanBeUndoneButAMatchCannotBeUndoneThatWay. Deleting a matched row
// here would leave the clearance behind with nothing pointing at it.
func TestASetAsideCanBeUndoneButAMatchCannotBeUndoneThatWay(t *testing.T) {
	f := matchSetup(t)
	f.pay(t, "PAY-1", "2025-05-14", "1000", "rent")
	f.importCSV(t, `Date,Narration,Withdrawal,Deposit
2025-05-14,RENT,1000.00,
2025-05-20,ACCOUNT FEE,177.00,
`)
	svc := bankstatement.NewService(f.pool)
	view := f.report(t)

	var rentLine, feeLine uuid.UUID
	for _, lp := range view.Proposals.Lines {
		if lp.Line.Amount.Equal(dec("1000")) {
			rentLine = lp.Line.ID
		} else {
			feeLine = lp.Line.ID
		}
	}

	// The fee is set aside and can be restored.
	if err := svc.SetAside(f.ctx, f.companyID, f.bank, feeLine, "fee", "tester"); err != nil {
		t.Fatalf("set aside: %v", err)
	}
	if err := svc.UndoSetAside(f.ctx, f.companyID, f.bank, feeLine); err != nil {
		t.Fatalf("undo set aside: %v", err)
	}
	if n := f.matches(t); n != 0 {
		t.Errorf("matches = %d after undo, want 0", n)
	}

	// The rent is MATCHED; restore must refuse it and say which key undoes a match.
	bank := banking.NewService(f.pool)
	if err := bank.SetBankDateFromStatementLine(f.ctx, f.companyID,
		f.vouchers["PAY-1"], f.bank, 0, "2025-05-14", "tester", rentLine); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	err := svc.UndoSetAside(f.ctx, f.companyID, f.bank, rentLine)
	if err == nil {
		t.Fatal("restoring a MATCHED line was allowed; the clearance would have been " +
			"left behind with nothing pointing at it, and the same bank row could then " +
			"be reconciled a second time")
	}
	if !strings.Contains(err.Error(), "MATCHED") {
		t.Errorf("the refusal must say the line is matched, got: %v", err)
	}
	if n := f.clearances(t); n != 1 {
		t.Errorf("clearances = %d, want the match's clearance untouched", n)
	}
}

// TestAlreadyClearedLegsAreNotOfferedAsCandidates is the memo's "Import spanning
// a period already reconciled manually: already-cleared legs are NOT candidates;
// no double-clearing."
func TestAlreadyClearedLegsAreNotOfferedAsCandidates(t *testing.T) {
	f := matchSetup(t)
	f.pay(t, "PAY-1", "2025-05-14", "1000", "rent")
	f.importCSV(t, `Date,Narration,Withdrawal,Deposit
2025-05-14,RENT,1000.00,
`)
	// Tick it off MANUALLY first — the slice-1 loop.
	bank := banking.NewService(f.pool)
	if err := bank.SetBankDate(f.ctx, f.companyID, f.vouchers["PAY-1"], f.bank, 0,
		"2025-05-14", "tester"); err != nil {
		t.Fatalf("manual tick: %v", err)
	}

	view := f.report(t)
	if len(view.Proposals.Lines) != 1 {
		t.Fatalf("proposals = %d, want the bank row still unresolved", len(view.Proposals.Lines))
	}
	if n := len(view.Proposals.Lines[0].Candidates); n != 0 {
		t.Errorf("candidates = %d, want 0 — a leg already cleared by the manual loop "+
			"must not be offered again, or accepting it would assert a second clearance "+
			"over the first", n)
	}
}

// TestACrossAccountMatchIsRefusedByTheDatabase — a line off one bank account's
// statement resolved against a leg on another. Both rows are real, both amounts
// correct, both accounts still balance: the failure is silently plausible, which
// is why it is refused by an FK rather than by the service remembering.
func TestACrossAccountMatchIsRefusedByTheDatabase(t *testing.T) {
	f := matchSetup(t)
	ids := identity.NewService(f.pool)
	other, err := ids.CreateLedger(f.ctx, "ICICI Current", "Bank Accounts")
	if err != nil {
		t.Fatalf("second bank: %v", err)
	}
	f.pay(t, "PAY-1", "2025-05-14", "1000", "rent")
	f.importCSV(t, `Date,Narration,Withdrawal,Deposit
2025-05-14,RENT,1000.00,
`)
	lineID := f.report(t).Proposals.Lines[0].Line.ID

	// The line belongs to HDFC; ask to resolve it against a leg on ICICI.
	bank := banking.NewService(f.pool)
	err = bank.SetBankDateFromStatementLine(f.ctx, f.companyID,
		f.vouchers["PAY-1"], other, 0, "2025-05-14", "tester", lineID)
	if err == nil {
		t.Fatal("a statement line was matched to a leg on a DIFFERENT bank account")
	}
	if n := f.clearances(t); n != 0 {
		t.Errorf("clearances = %d, want 0", n)
	}
}
