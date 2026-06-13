// Package migration is the Tally-XML migration importer and rupee
// reconciliation harness — the switching-cost killer that lets a prospect move
// their existing books into cored and prove, to the paisa, that nothing changed.
//
// The importer parses a Tally ENVELOPE export (RESEARCH sec 19), ensures every
// referenced ledger exists (creating it on demand under a sensible account
// group via the masters API), and posts each voucher through the foundation
// ledger.PostVoucher path. That path is the SOLE writer of the books, so every
// imported voucher inherits the same guarantees a hand-keyed one does: the
// company-scoped GUC guard, the immutable BLAKE3 hash-chained voucher_version,
// and the deferred double-entry (debit == credit) trigger. The importer adds no
// new write path and bypasses none of those invariants.
//
// After an import, the reconciliation harness rolls up the Trial Balance via the
// reporting service and compares it, exactly (NUMERIC(19,4), to the paisa),
// against the expected balances shipped with the corpus. Any mismatch — a
// missing ledger, a dropped voucher, a sign flip — fails loudly. A clean
// reconcile is the proof the migration is lossless.
package migration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/integration"
	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/masters"
	"github.com/khata/cored/internal/reporting"
)

// classByVchType maps a Tally voucher type/class label to a cored accounting
// class. Tally users may rename voucher types, so VOUCHERTYPENAME is not relied
// upon for the class; the canonical VCHTYPE attribute is matched
// case-insensitively against the six built-in classes.
var classByVchType = map[string]ledger.Class{
	"contra":   ledger.Contra,
	"payment":  ledger.Payment,
	"receipt":  ledger.Receipt,
	"journal":  ledger.Journal,
	"sales":    ledger.Sales,
	"purchase": ledger.Purchase,
}

// Importer migrates Tally XML exports into cored, creating masters on demand and
// posting vouchers through the ledger service.
type Importer struct {
	masters *masters.Service
	ledger  *ledger.Service
}

// NewImporter constructs an Importer over the masters and ledger services. Both
// must be backed by the same tenant-scoped pool; the importer never opens its
// own database connection.
func NewImporter(m *masters.Service, l *ledger.Service) *Importer {
	return &Importer{masters: m, ledger: l}
}

// ImportResult summarises an import run.
type ImportResult struct {
	// SourceCompany is the SVCURRENTCOMPANY name recorded in the export header.
	SourceCompany string
	// VouchersPosted is the number of vouchers successfully posted.
	VouchersPosted int
	// LedgersEnsured is the set of distinct ledger names referenced (created or
	// already present), in stable order.
	LedgersEnsured []string
	// Posted holds one PostResult per posted voucher, in document order.
	Posted []ledger.PostResult
}

// ImportReader parses a Tally XML export from r and imports every VOUCHER
// message into the company in ctx. It is the convenience entry point over a
// stream; see ImportFile for a path.
func (im *Importer) ImportReader(ctx context.Context, r io.Reader, createdBy string) (ImportResult, error) {
	env, err := integration.ParseTallyEnvelope(r)
	if err != nil {
		return ImportResult{}, err
	}
	return im.ImportEnvelope(ctx, env, createdBy)
}

// ImportFile reads and imports a Tally XML export from a filesystem path.
func (im *Importer) ImportFile(ctx context.Context, path, createdBy string) (ImportResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return ImportResult{}, fmt.Errorf("migration: open %s: %w", path, err)
	}
	defer f.Close()
	return im.ImportReader(ctx, f, createdBy)
}

// ImportEnvelope imports an already-parsed Tally envelope. Vouchers are posted
// in document order; the first error aborts the run (each voucher is its own
// transaction, so vouchers committed before the failure remain — callers
// migrating production data should import into a fresh company and reconcile
// before going live).
func (im *Importer) ImportEnvelope(ctx context.Context, env *integration.TallyEnvelope, createdBy string) (ImportResult, error) {
	if env == nil {
		return ImportResult{}, fmt.Errorf("migration: nil envelope")
	}
	if createdBy == "" {
		createdBy = "tally-import"
	}
	res := ImportResult{SourceCompany: env.Body.Desc.StaticVariables.CurrentCompany}

	// ledgerIDs memoises name->id within this run so a ledger referenced by
	// several vouchers is resolved/created once.
	ledgerIDs := make(map[string]uuid.UUID)
	seenLedger := make(map[string]bool)

	for i, msg := range env.Body.Data.Messages {
		if msg.Voucher == nil {
			continue // non-voucher messages (masters, etc.) are ignored in Phase 1
		}
		v := msg.Voucher
		// Action defaults to Create; only creates are supported on import.
		if act := strings.ToUpper(strings.TrimSpace(v.Action)); act != "" && act != "CREATE" {
			return res, fmt.Errorf("migration: voucher %d: unsupported ACTION %q (only Create)", i+1, v.Action)
		}

		posted, names, err := im.importVoucher(ctx, i, v, ledgerIDs, createdBy)
		if err != nil {
			return res, err
		}
		for _, n := range names {
			if !seenLedger[n] {
				seenLedger[n] = true
				res.LedgersEnsured = append(res.LedgersEnsured, n)
			}
		}
		res.Posted = append(res.Posted, posted)
		res.VouchersPosted++
	}
	return res, nil
}

// importVoucher ensures the voucher's ledgers exist and posts it. It returns the
// post result and the list of ledger names it touched.
func (im *Importer) importVoucher(ctx context.Context, idx int, v *integration.TallyVoucher,
	ledgerIDs map[string]uuid.UUID, createdBy string) (ledger.PostResult, []string, error) {

	class, ok := classByVchType[strings.ToLower(strings.TrimSpace(v.VchType))]
	if !ok {
		// Fall back to the (possibly renamed) voucher-type name before giving up.
		class, ok = classByVchType[strings.ToLower(strings.TrimSpace(v.VoucherTypeName))]
	}
	if !ok {
		return ledger.PostResult{}, nil, fmt.Errorf(
			"migration: voucher %d: unrecognised VCHTYPE %q", idx+1, v.VchType)
	}

	entries := v.Entries()
	if len(entries) < 2 {
		return ledger.PostResult{}, nil, fmt.Errorf(
			"migration: voucher %d: needs at least two ledger entries, got %d", idx+1, len(entries))
	}

	isoDate, err := integration.TallyDateToISO(v.Date)
	if err != nil {
		return ledger.PostResult{}, nil, fmt.Errorf("migration: voucher %d: %w", idx+1, err)
	}

	var (
		postings []ledger.PostingInput
		names    []string
	)
	for _, e := range entries {
		name := strings.TrimSpace(e.LedgerName)
		if name == "" {
			return ledger.PostResult{}, nil, fmt.Errorf("migration: voucher %d: empty ledger name", idx+1)
		}
		debit, err := e.Side()
		if err != nil {
			return ledger.PostResult{}, nil, fmt.Errorf("migration: voucher %d: %w", idx+1, err)
		}
		amount, err := e.Magnitude()
		if err != nil {
			return ledger.PostResult{}, nil, fmt.Errorf("migration: voucher %d: %w", idx+1, err)
		}

		drcr := ledger.Credit
		if debit {
			drcr = ledger.Debit
		}

		id, err := im.ensureLedger(ctx, name, class, drcr, ledgerIDs, createdBy)
		if err != nil {
			return ledger.PostResult{}, nil, fmt.Errorf("migration: voucher %d: ledger %q: %w", idx+1, name, err)
		}
		postings = append(postings, ledger.PostingInput{LedgerID: id, DrCr: drcr, Amount: amount})
		names = append(names, name)
	}

	// Resolve the voucher type for this class (the six are seeded per company).
	vtID, err := im.masters.VoucherTypeID(ctx, string(class))
	if err != nil {
		return ledger.PostResult{}, nil, fmt.Errorf(
			"migration: voucher %d: resolve voucher type %q: %w", idx+1, class, err)
	}

	in := ledger.VoucherInput{
		Class:         class,
		VoucherTypeID: vtID,
		VoucherNo:     strings.TrimSpace(v.VoucherNumber),
		VoucherDate:   isoDate,
		Narration:     strings.TrimSpace(v.Narration),
		Party:         strings.TrimSpace(v.PartyLedgerName),
		CreatedBy:     createdBy,
		Postings:      postings,
	}
	res, err := im.ledger.PostVoucher(ctx, in)
	if err != nil {
		return ledger.PostResult{}, nil, fmt.Errorf("migration: voucher %d: post: %w", idx+1, err)
	}
	return res, names, nil
}

// ensureLedger resolves a ledger by name, creating it (under an inferred group)
// if absent, memoising the id for the run.
func (im *Importer) ensureLedger(ctx context.Context, name string, class ledger.Class,
	side ledger.DrCr, cache map[string]uuid.UUID, createdBy string) (uuid.UUID, error) {

	if id, ok := cache[name]; ok {
		return id, nil
	}
	group := inferGroup(name, class, side)
	id, err := im.masters.EnsureLedger(ctx, name, group, createdBy)
	if err != nil {
		return uuid.Nil, err
	}
	cache[name] = id
	return id, nil
}

// inferGroup picks a sensible Tally account group for a ledger that does not yet
// exist, given its name and how it is being used (the voucher class and the side
// it posts on). The seeded "Cash" ledger is never created here (EnsureLedger
// finds it first); this only fires for genuinely new ledgers in the export.
//
// The mapping mirrors how a Tally user would group these accounts (RESEARCH
// sec 5): cash/bank by name, then class+side. It is deliberately conservative —
// the exact group does not change the Trial Balance debit/credit split, but it
// gives imported ledgers a correct Balance-Sheet vs P&L home.
func inferGroup(name string, class ledger.Class, side ledger.DrCr) string {
	lower := strings.ToLower(name)
	switch {
	case lower == "cash" || strings.Contains(lower, "cash"):
		return "Cash-in-Hand"
	case strings.Contains(lower, "bank"):
		return "Bank Accounts"
	case strings.Contains(lower, "capital"):
		return "Capital Account"
	case strings.Contains(lower, "gst") || strings.Contains(lower, "cgst") ||
		strings.Contains(lower, "sgst") || strings.Contains(lower, "igst") ||
		strings.Contains(lower, "tax") || strings.Contains(lower, "duty"):
		return "Duties & Taxes"
	}

	switch class {
	case ledger.Sales:
		if side == ledger.Credit {
			return "Sales Accounts"
		}
		return "Sundry Debtors"
	case ledger.Purchase:
		if side == ledger.Debit {
			return "Purchase Accounts"
		}
		return "Sundry Creditors"
	case ledger.Receipt:
		// Money received: the non-cash side is the source — typically capital or
		// income. Capital is handled by name above; default the rest to income.
		if side == ledger.Credit {
			return "Indirect Incomes"
		}
		return "Sundry Debtors"
	case ledger.Payment:
		// Money paid: the non-cash side is the expense/creditor being settled.
		if side == ledger.Debit {
			return "Indirect Expenses"
		}
		return "Sundry Creditors"
	case ledger.Contra:
		return "Bank Accounts"
	default: // Journal and anything else.
		if side == ledger.Debit {
			return "Indirect Expenses"
		}
		return "Indirect Incomes"
	}
}

// --------------------------------------------------------------------------
// Reconciliation harness
// --------------------------------------------------------------------------

// ExpectedBalance is one ledger's expected Trial Balance line, as shipped in the
// migration corpus JSON. Debit/Credit are fixed-4 NUMERIC(19,4) strings (paisa
// precision).
type ExpectedBalance struct {
	Ledger string `json:"ledger"`
	Debit  string `json:"debit"`
	Credit string `json:"credit"`
}

// ExpectedTotals is the expected Trial Balance column totals.
type ExpectedTotals struct {
	Debit    string `json:"debit"`
	Credit   string `json:"credit"`
	Balanced bool   `json:"balanced"`
}

// ExpectedTrialBalance is the parsed migration corpus expectation document
// (corpora/migration/*.json).
type ExpectedTrialBalance struct {
	ID                   string            `json:"id"`
	Description          string            `json:"description"`
	TallyExportFile      string            `json:"tallyExportFile"`
	ExpectedTrialBalance []ExpectedBalance `json:"expectedTrialBalance"`
	ExpectedTotals       ExpectedTotals    `json:"expectedTotals"`
}

// LoadExpected parses a migration corpus expectation JSON document.
func LoadExpected(r io.Reader) (ExpectedTrialBalance, error) {
	var e ExpectedTrialBalance
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&e); err != nil {
		return ExpectedTrialBalance{}, fmt.Errorf("migration: decode expected trial balance: %w", err)
	}
	return e, nil
}

// LoadExpectedFile parses a migration corpus expectation JSON from a path.
func LoadExpectedFile(path string) (ExpectedTrialBalance, error) {
	f, err := os.Open(path)
	if err != nil {
		return ExpectedTrialBalance{}, fmt.Errorf("migration: open %s: %w", path, err)
	}
	defer f.Close()
	return LoadExpected(f)
}

// Reconcile computes the Trial Balance for the company in ctx via the reporting
// service and compares it, exactly to the paisa, against expected. It returns
// nil only when every expected ledger balance, every column total, and the
// balanced flag match AND no unexpected ledger carries a non-zero balance. On
// any divergence it returns a multi-line error naming each discrepancy.
//
// This is the switching-cost gate: a non-nil return means the migration is NOT
// faithful and must not be trusted.
func Reconcile(ctx context.Context, rep *reporting.Service, companyID uuid.UUID, expected ExpectedTrialBalance) error {
	tb, err := rep.TrialBalance(ctx, companyID)
	if err != nil {
		return fmt.Errorf("migration: reconcile: trial balance: %w", err)
	}

	// Index the actual balances by ledger name for lookup, summing defensively in
	// case the report ever returns split rows for one ledger.
	type bal struct{ debit, credit decimal.Decimal }
	actual := make(map[string]bal, len(tb.Rows))
	for _, r := range tb.Rows {
		b := actual[r.LedgerName]
		b.debit = b.debit.Add(r.Debit)
		b.credit = b.credit.Add(r.Credit)
		actual[r.LedgerName] = b
	}

	var problems []string
	matched := make(map[string]bool, len(expected.ExpectedTrialBalance))

	for _, want := range expected.ExpectedTrialBalance {
		matched[want.Ledger] = true
		wantDr, err := decimal.NewFromString(want.Debit)
		if err != nil {
			return fmt.Errorf("migration: reconcile: bad expected debit %q for %q: %w", want.Debit, want.Ledger, err)
		}
		wantCr, err := decimal.NewFromString(want.Credit)
		if err != nil {
			return fmt.Errorf("migration: reconcile: bad expected credit %q for %q: %w", want.Credit, want.Ledger, err)
		}
		got, ok := actual[want.Ledger]
		if !ok {
			problems = append(problems, fmt.Sprintf(
				"ledger %q missing from imported books (expected Dr %s / Cr %s)",
				want.Ledger, wantDr.StringFixed(4), wantCr.StringFixed(4)))
			continue
		}
		if !got.debit.Equal(wantDr) || !got.credit.Equal(wantCr) {
			problems = append(problems, fmt.Sprintf(
				"ledger %q: got Dr %s / Cr %s, expected Dr %s / Cr %s",
				want.Ledger, got.debit.StringFixed(4), got.credit.StringFixed(4),
				wantDr.StringFixed(4), wantCr.StringFixed(4)))
		}
	}

	// Any actual ledger with a non-zero balance that the expectation did not
	// mention is an unexpected extra — flag it (a zero-balance system ledger such
	// as a seeded one is tolerated).
	for name, b := range actual {
		if matched[name] {
			continue
		}
		if !b.debit.IsZero() || !b.credit.IsZero() {
			problems = append(problems, fmt.Sprintf(
				"unexpected ledger %q with non-zero balance Dr %s / Cr %s",
				name, b.debit.StringFixed(4), b.credit.StringFixed(4)))
		}
	}

	// Column totals and the balanced flag.
	wantTotDr, err := decimal.NewFromString(expected.ExpectedTotals.Debit)
	if err != nil {
		return fmt.Errorf("migration: reconcile: bad expected total debit %q: %w", expected.ExpectedTotals.Debit, err)
	}
	wantTotCr, err := decimal.NewFromString(expected.ExpectedTotals.Credit)
	if err != nil {
		return fmt.Errorf("migration: reconcile: bad expected total credit %q: %w", expected.ExpectedTotals.Credit, err)
	}
	if !tb.TotalDebit.Equal(wantTotDr) {
		problems = append(problems, fmt.Sprintf(
			"total debit: got %s, expected %s", tb.TotalDebit.StringFixed(4), wantTotDr.StringFixed(4)))
	}
	if !tb.TotalCredit.Equal(wantTotCr) {
		problems = append(problems, fmt.Sprintf(
			"total credit: got %s, expected %s", tb.TotalCredit.StringFixed(4), wantTotCr.StringFixed(4)))
	}
	if tb.IsBalanced() != expected.ExpectedTotals.Balanced {
		problems = append(problems, fmt.Sprintf(
			"balanced flag: got %v, expected %v", tb.IsBalanced(), expected.ExpectedTotals.Balanced))
	}

	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("migration: reconciliation FAILED (%d discrepancies):\n  - %s",
			len(problems), strings.Join(problems, "\n  - "))
	}
	return nil
}
