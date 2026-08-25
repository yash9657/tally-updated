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
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/compliance"
	"github.com/khata/cored/internal/gststate"
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
	// VouchersPosted is the number of vouchers successfully posted (created).
	VouchersPosted int
	// VouchersAltered / VouchersDeleted count ACTION=Alter / ACTION=Delete
	// messages that were routed (by Tally GUID) to AlterVoucher / DeleteVoucher.
	VouchersAltered int
	VouchersDeleted int
	// GroupsEnsured names the account groups created from GROUP masters, in
	// document order. Groups already present (Tally's own built-ins, which every
	// cored company is seeded with) are resolved, not created, and not listed.
	GroupsEnsured []string
	// LedgersEnsured is the set of distinct ledger names referenced (created or
	// already present), in stable order.
	LedgersEnsured []string
	// Warnings records everything the import decided to carry on through rather
	// than abort over — a ledger whose Tally group is not in the export, an
	// opening bill-wise breakup that does not add up to its own opening balance.
	// ⛔ A CLEAN IMPORT IS NOT THE SAME AS AN EMPTY Warnings, and an operator who
	// never reads them has migrated something subtly different from their books.
	Warnings []string
	// Posted holds one PostResult per posted voucher, in document order.
	Posted []ledger.PostResult
	// Skipped is the count of vouchers that were NOT posted because a voucher with
	// the same Tally GUID had already been imported into this company (idempotent
	// re-import). SkippedGUIDs lists those GUIDs in document order.
	Skipped      int
	SkippedGUIDs []string

	// ---- THE GOODS (inventory.go) ------------------------------------------
	//
	// ⛔ THESE COUNTS ARE THE ONLY VISIBLE EVIDENCE THAT THE IMPORT BROUGHT IN ANY
	// STOCK AT ALL, and that is why they are reported rather than left to a query.
	// Quantity is INVISIBLE IN A TRIAL BALANCE: an import that reads every stock
	// line and throws it away produces books that balance perfectly and a Stock
	// Summary with nothing in it — which is exactly what this importer did until
	// the lane that added them. A migration whose report says "227 stock lines"
	// can be checked; one that says "121 vouchers posted" cannot.
	//
	// Each name list holds the masters CREATED (not merely referenced), sorted
	// and de-duplicated, so a 121-voucher file does not report one item forty
	// times.
	StockItemsEnsured []string
	UnitsEnsured      []string
	GodownsEnsured    []string
	BatchesEnsured    []string
	// OpeningPositions is the number of stock_opening rows written from STOCKITEM
	// masters. ⚠️ ZERO HERE ON A FILE WITH STOCK LINES IS A WARNING SIGN, not a
	// clean result: it means the export carried no opening stock, so every
	// position starts at nil and the first issue of older goods has nothing to
	// consume. See describeNegativeStock.
	OpeningPositions int
	// StockLines is the number of stock MOVEMENTS written across every voucher.
	StockLines int
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
	res := ImportResult{SourceCompany: env.SourceCompany()}

	// ledgerIDs memoises name->id within this run so a ledger referenced by
	// several vouchers is resolved/created once.
	ledgerIDs := make(map[string]uuid.UUID)
	seenLedger := make(map[string]bool)
	// ledgerState carries the two-digit GST state code of each named ledger as
	// declared by its LEDGER master, used to derive a Sales voucher's place of
	// supply when the voucher itself does not carry one.
	ledgerState := make(map[string]string)

	// PASS ZERO — GROUP masters, before any ledger can name one as its parent.
	// A real export carries the whole chart of accounts, and a company that has
	// been trading for years has ADDED to it: the file tested against declared 37
	// groups, 9 of them the user's own ("DALALI EXPS.", "SUNDRY DEBTORS -
	// OUTSTATION", "PURCHASES-OMS"), and 217 ledgers hung off those nine. Without
	// this pass every one of them failed to create — masters.CreateLedger
	// resolves the group by name and refuses ErrNotFound — and the import died on
	// the first of them.
	groupCanon, groupsMade, groupWarns, err := im.importGroups(ctx, env.Messages(), createdBy)
	if err != nil {
		return res, err
	}
	res.GroupsEnsured = groupsMade
	res.Warnings = append(res.Warnings, groupWarns...)

	// PASS ZERO-AND-A-HALF — THE INVENTORY MASTERS, before any voucher can name
	// one: units, then locations, then items with their OPENING POSITIONS.
	//
	// ⛔ IT RUNS BEFORE THE LEDGER PASS AS WELL AS BEFORE THE VOUCHERS, and the
	// reason is the opening stock. An opening position must exist before the first
	// issue of that item, and the first issue is a voucher; putting this after the
	// ledgers costs nothing and putting it after the vouchers would mean every
	// position was already negative by the time its opening arrived — with the
	// issues already valued at nil cost, which no later row can repair, because
	// the cost of goods sold is derived from the state AT THE MOVEMENT.
	//
	// ⚠️ A file with no inventory masters at all (a Day Book export — the real one
	// this was built against) does nothing here and imports its goods from the
	// vouchers themselves. See inventory.go.
	run := cctx{ctx: ctx, createdBy: createdBy, inv: newInventoryCache()}
	if err := im.importInventoryMasters(run, env.Messages(), &res); err != nil {
		return res, err
	}

	// FIRST PASS — LEDGER masters: opening balances + GST master attributes. These
	// must land before any voucher posts so (a) reporting folds the opening
	// balances into TB/BS/P&L and (b) the GSTR-1 party join classifies B2B vs B2C.
	// A master whose ledger already exists is left unchanged (resolve-first).
	for i, msg := range env.Messages() {
		if msg.Ledger == nil {
			continue
		}
		id, name, state, warns, err := im.importLedgerMaster(ctx, i, msg.Ledger, ledgerIDs, groupCanon, createdBy)
		if err != nil {
			return res, err
		}
		res.Warnings = append(res.Warnings, warns...)
		if state != "" {
			ledgerState[name] = state
		}
		if !seenLedger[name] {
			seenLedger[name] = true
			res.LedgersEnsured = append(res.LedgersEnsured, name)
		}
		ledgerIDs[name] = id
	}

	// recordLedgers folds the names referenced by a posted/altered voucher into
	// the run's distinct ensured-ledger set, in stable first-seen order.
	recordLedgers := func(names []string) {
		for _, n := range names {
			if !seenLedger[n] {
				seenLedger[n] = true
				res.LedgersEnsured = append(res.LedgersEnsured, n)
			}
		}
	}

	// SECOND PASS — vouchers, in document order. The Tally ACTION drives the
	// dispatch: Create posts a new voucher (idempotent on GUID), Alter appends a
	// new immutable version to the voucher previously imported under that GUID,
	// and Delete tombstones it. Alter/Delete resolve their target by the stable
	// Tally GUID, mirroring how Tally itself re-syncs an edited/removed voucher.
	for i, msg := range env.Messages() {
		if msg.Voucher == nil {
			continue // ledger masters handled in the first pass
		}
		v := msg.Voucher
		action := strings.ToUpper(strings.TrimSpace(v.Action))
		if action == "" {
			action = "CREATE"
		}
		guid := v.GUIDValue()

		switch action {
		case "DELETE":
			// Tombstone the voucher previously imported under this GUID. With no
			// GUID there is nothing to resolve; with an unknown GUID there is
			// nothing to delete (recorded as skipped).
			if guid == "" {
				return res, fmt.Errorf("migration: voucher %d: ACTION=Delete requires a GUID", i+1)
			}
			vid, found, err := im.ledger.VoucherIDBySourceGUID(ctx, guid)
			if err != nil {
				return res, fmt.Errorf("migration: voucher %d: delete lookup: %w", i+1, err)
			}
			if !found {
				res.Skipped++
				res.SkippedGUIDs = append(res.SkippedGUIDs, guid)
				continue
			}
			if _, err := im.ledger.DeleteVoucher(ctx, vid, createdBy); err != nil {
				return res, fmt.Errorf("migration: voucher %d: delete: %w", i+1, err)
			}
			res.VouchersDeleted++

		case "ALTER":
			in, names, err := im.buildVoucherInput(run, i, v, ledgerIDs, ledgerState, &res)
			if err != nil {
				return res, err
			}
			var (
				vid   uuid.UUID
				found bool
			)
			if guid != "" {
				if vid, found, err = im.ledger.VoucherIDBySourceGUID(ctx, guid); err != nil {
					return res, fmt.Errorf("migration: voucher %d: alter lookup: %w", i+1, err)
				}
			}
			if found {
				posted, err := im.ledger.AlterVoucher(ctx, vid, in)
				if err != nil {
					return res, postError(i, v, "alter", err)
				}
				recordLedgers(names)
				res.Posted = append(res.Posted, posted)
				res.VouchersAltered++
			} else {
				// No prior version under this GUID: an Alter with nothing to amend
				// is treated as a create (the voucher simply did not arrive first).
				posted, err := im.ledger.PostVoucher(ctx, in)
				if err != nil {
					return res, postError(i, v, "post", err)
				}
				recordLedgers(names)
				res.Posted = append(res.Posted, posted)
				res.VouchersPosted++
			}

		default: // CREATE
			// Idempotency: a voucher carrying a GUID already imported into this
			// company is skipped (no re-post), matching Tally's same-GUID
			// semantics. A GUID-less voucher is never deduped — it always posts.
			if guid != "" {
				if _, found, err := im.ledger.VoucherIDBySourceGUID(ctx, guid); err != nil {
					return res, fmt.Errorf("migration: voucher %d: idempotency lookup: %w", i+1, err)
				} else if found {
					res.Skipped++
					res.SkippedGUIDs = append(res.SkippedGUIDs, guid)
					continue
				}
			}
			in, names, err := im.buildVoucherInput(run, i, v, ledgerIDs, ledgerState, &res)
			if err != nil {
				return res, err
			}
			posted, err := im.ledger.PostVoucher(ctx, in)
			if err != nil {
				return res, postError(i, v, "post", err)
			}
			recordLedgers(names)
			res.Posted = append(res.Posted, posted)
			res.VouchersPosted++
		}
	}
	// One item named on forty invoices is ONE line in the report. Sorted so two
	// runs of the same file produce the same report and a diff means something.
	res.StockItemsEnsured = sortedUnique(res.StockItemsEnsured)
	res.UnitsEnsured = sortedUnique(res.UnitsEnsured)
	res.GodownsEnsured = sortedUnique(res.GodownsEnsured)
	res.BatchesEnsured = sortedUnique(res.BatchesEnsured)
	return res, nil
}

// postError decorates a post/alter failure. A NEGATIVE-STOCK refusal gets its own
// sentence, because it is the one failure a REAL export causes routinely and the
// one an operator can do something about — see describeNegativeStock. Everything
// else keeps the message it always had.
func postError(idx int, v *integration.TallyVoucher, verb string, err error) error {
	var neg *ledger.NegativeStockError
	if errors.As(err, &neg) {
		return describeNegativeStock(idx, v, err)
	}
	return fmt.Errorf("migration: voucher %d: %s: %w", idx+1, verb, err)
}

// importGroups ensures every GROUP master in the export exists as an account
// group, parents before children, and returns a map from the Tally spelling
// (lower-cased) to the name as STORED — which is what every later lookup must
// use, because masters resolves a ledger's group by exact name.
//
// Tally's own built-in groups are seeded into every cored company already, so
// they resolve rather than create. That resolution is deliberately
// case-insensitive: Tally exports "Cash-in-hand" and "Stock-in-hand" where the
// seed spells them "Cash-in-Hand" and "Stock-in-Hand", and account_group has no
// normalised unique index to stop a second copy landing beside the first.
//
// A group whose parent is missing from the export is REPORTED, not invented: a
// primary group needs a nature (Balance Sheet vs P&L) that nothing in the file
// supplies, and guessing it would misfile every ledger beneath it.
func (im *Importer) importGroups(ctx context.Context, msgs []integration.TallyMessage,
	createdBy string) (map[string]string, []string, []string, error) {

	canon := make(map[string]string)
	var made, warns []string

	pending := make([]*integration.TallyGroupMaster, 0, len(msgs))
	for _, m := range msgs {
		if m.Group == nil || m.Group.Deleted() || m.Group.Name() == "" {
			continue
		}
		pending = append(pending, m.Group)
	}

	// resolve returns the stored spelling of a group already known to the
	// company (or to this run), and whether it was found.
	resolve := func(name string) (string, bool, error) {
		key := strings.ToLower(strings.TrimSpace(name))
		if stored, ok := canon[key]; ok {
			return stored, true, nil
		}
		_, stored, err := im.masters.GroupIDByName(ctx, name)
		switch {
		case err == nil:
			canon[key] = stored
			return stored, true, nil
		case errors.Is(err, masters.ErrNotFound):
			return "", false, nil
		default:
			return "", false, err
		}
	}

	// Parents before children, without assuming the export is ordered that way:
	// keep sweeping while any group resolves, then report whatever is left.
	for len(pending) > 0 {
		var deferred []*integration.TallyGroupMaster
		progress := false
		for _, g := range pending {
			name := g.Name()
			if _, found, err := resolve(name); err != nil {
				return nil, nil, nil, fmt.Errorf("migration: group %q: %w", name, err)
			} else if found {
				progress = true
				continue
			}
			parent := strings.TrimSpace(g.Parent)
			if parent == "" {
				// A primary group cored does not already have. Its nature is not in
				// the file, and a wrong nature moves ledgers between the Balance
				// Sheet and the P&L, so this one is named rather than guessed.
				warns = append(warns, fmt.Sprintf(
					"group %q is a primary group this company does not have and the export "+
						"does not state its nature; ledgers under it will be grouped by inference", name))
				progress = true
				continue
			}
			storedParent, found, err := resolve(parent)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("migration: group %q: parent %q: %w", name, parent, err)
			}
			if !found {
				deferred = append(deferred, g) // parent may be declared later in the file
				continue
			}
			_, stored, err := im.masters.EnsureGroup(ctx,
				masters.Group{Name: name, ParentName: storedParent}, createdBy)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("migration: group %q under %q: %w", name, storedParent, err)
			}
			canon[strings.ToLower(strings.TrimSpace(name))] = stored
			made = append(made, stored)
			progress = true
		}
		if !progress {
			for _, g := range deferred {
				warns = append(warns, fmt.Sprintf(
					"group %q names a parent %q that is neither in this company nor in the export; "+
						"ledgers under it will be grouped by inference", g.Name(), strings.TrimSpace(g.Parent)))
			}
			break
		}
		pending = deferred
	}
	return canon, made, warns, nil
}

// groupNameFor maps a Tally PARENT onto the account group name as stored in this
// company, resolving case-insensitively and consulting the groups this run
// created. It returns ok=false when the group is not in the company at all —
// which happens when the export carried ledgers but not the GROUP masters they
// name (an "Accounting Masters only" export, or a Day Book on its own).
func (im *Importer) groupNameFor(ctx context.Context, parent string,
	canon map[string]string) (string, bool, error) {

	parent = strings.TrimSpace(parent)
	if parent == "" {
		return "", false, nil
	}
	if stored, ok := canon[strings.ToLower(parent)]; ok {
		return stored, true, nil
	}
	_, stored, err := im.masters.GroupIDByName(ctx, parent)
	switch {
	case err == nil:
		canon[strings.ToLower(parent)] = stored
		return stored, true, nil
	case errors.Is(err, masters.ErrNotFound):
		return "", false, nil
	default:
		return "", false, err
	}
}

// importLedgerMaster applies one Tally LEDGER master: it ensures the ledger
// exists with its opening balance (set as ledger.opening_balance/opening_dc so
// reporting folds it into the statements with zero reporting change), its
// opening bill-wise breakup, and its GST master attributes (GSTIN / state_code /
// registration_type). An already-present ledger is left unchanged. It returns the
// ledger id, resolved name, the declared state code (empty if none) and any
// warnings raised.
func (im *Importer) importLedgerMaster(ctx context.Context, idx int, m *integration.TallyLedgerMaster,
	cache map[string]uuid.UUID, groupCanon map[string]string, createdBy string) (
	uuid.UUID, string, string, []string, error) {

	var warns []string
	name := m.Name()
	if name == "" {
		return uuid.Nil, "", "", nil, fmt.Errorf("migration: ledger master %d: empty NAME", idx+1)
	}
	stateCode := stateCodeFor(m)
	if id, ok := cache[name]; ok {
		return id, name, stateCode, nil, nil
	}

	debit, hasOpening, err := m.OpeningSide()
	if err != nil {
		return uuid.Nil, "", "", nil, fmt.Errorf("migration: ledger master %d: %w", idx+1, err)
	}
	mag, err := m.OpeningMagnitude()
	if err != nil {
		return uuid.Nil, "", "", nil, fmt.Errorf("migration: ledger master %d: %w", idx+1, err)
	}

	openDC := "C"
	if debit {
		openDC = "D"
	}
	openBal := decimal.Zero
	if hasOpening {
		openBal = mag
	}

	group, found, err := im.groupNameFor(ctx, m.Parent, groupCanon)
	if err != nil {
		return uuid.Nil, "", "", nil, fmt.Errorf("migration: ledger master %d: group: %w", idx+1, err)
	}
	if !found {
		// No usable Tally parent: infer a home from the opening side (debit-natured
		// => asset bucket, credit-natured => liability bucket) by name.
		side := ledger.Credit
		if debit {
			side = ledger.Debit
		}
		group = inferGroup(name, ledger.Journal, side)
		if p := strings.TrimSpace(m.Parent); p != "" {
			warns = append(warns, fmt.Sprintf(
				"ledger %q names Tally group %q, which is not in this company and not in the "+
					"export; it was grouped under %q by inference", name, p, group))
		}
	}

	l := masters.Ledger{
		Name:             name,
		GroupName:        group,
		OpeningBalance:   openBal,
		OpeningDC:        openDC,
		GSTIN:            m.GSTINValue(),
		StateCode:        stateCode,
		RegistrationType: masterRegType(m),
	}
	id, err := im.masters.EnsureLedgerWithOpening(ctx, l, createdBy)
	if err != nil {
		return uuid.Nil, "", "", nil, fmt.Errorf("migration: ledger master %d: ensure %q: %w", idx+1, name, err)
	}

	if hasOpening {
		w, err := im.applyOpening(ctx, m, id, openBal, openDC, createdBy)
		if err != nil {
			return uuid.Nil, "", "", nil, fmt.Errorf(
				"migration: ledger master %d: opening for %q: %w", idx+1, name, err)
		}
		if w != "" {
			warns = append(warns, w)
		}
	}
	return id, name, l.StateCode, warns, nil
}

// applyOpeningBills writes the bill-wise breakup of a ledger's OPENING balance.
//
// ⛔ WITHOUT IT A MIGRATED DEBTOR HAS THE RIGHT TOTAL AND NO AGEING AT ALL. The
// opening balance is one number; the Outstandings report buckets by BILL, so
// every rupee a business was owed on the day it switched would sit outside
// 0-30/31-60/61-90 with no invoice against it — which is precisely the report a
// migrating business checks first.
//
// The breakup is attached only when it actually reconciles: masters requires it
// to sum to the opening balance on one side, and a real file does not always
// oblige. Of the 156 ledgers carrying named opening bills in the export tested
// against, 139 summed exactly, 17 did not, and 5 mixed debit and credit refs on
// one ledger (a debtor holding advances — a position cored's opening cannot
// express). Those are reported and the TOTAL is kept: a right balance with a
// blank ageing beats a wrong balance.
func (im *Importer) applyOpening(ctx context.Context, m *integration.TallyLedgerMaster,
	ledgerID uuid.UUID, openBal decimal.Decimal, openDC, createdBy string) (string, error) {

	// ⛔ THE OPENING MUST BE WRITTEN EVEN WHEN THE LEDGER WAS ALREADY THERE, and
	// that is the whole reason this reads before it writes. Every company is
	// seeded with a handful of ledgers — Cash among them — so the export's Cash
	// master resolves to one that exists, EnsureLedgerWithOpening returns it
	// UNCHANGED, and the opening cash balance is dropped on the floor. In the real
	// export tested against that was ONE ledger and 14,14,596.01 rupees, and the
	// only reason it was caught is that a Trial Balance which no longer balances
	// is impossible to miss. Had it been a debtor rather than cash, the sheet
	// would still have balanced and simply been wrong.
	//
	// An existing NON-ZERO opening is never overwritten: that is somebody's
	// already-migrated position, and a second import must not restate it.
	current, _, err := im.masters.LedgerOpening(ctx, ledgerID)
	if err != nil {
		return "", err
	}
	if !current.IsZero() && !current.Equal(openBal) {
		return fmt.Sprintf(
			"ledger %q already carries an opening balance of %s in this company; the export's %s "+
				"was NOT applied", m.Name(), current.StringFixed(2), openBal.StringFixed(2)), nil
	}

	var (
		bills []masters.OpeningBill
		sum   decimal.Decimal
	)
	for _, b := range m.OpeningBills {
		ref := strings.TrimSpace(b.Name)
		if ref == "" {
			continue
		}
		raw := strings.TrimSpace(b.OpeningBalance)
		if raw == "" {
			continue
		}
		amt, err := decimal.NewFromString(raw)
		if err != nil {
			return "", fmt.Errorf("bill %q: bad OPENINGBALANCE %q: %w", ref, raw, err)
		}
		if amt.IsZero() {
			continue
		}
		// Same signed convention as the ledger's own opening: negative = debit.
		dc := "C"
		if amt.IsNegative() {
			dc = "D"
		}
		if dc != openDC {
			return fmt.Sprintf(
					"ledger %q: opening bill %q is a %s against a %s opening balance; cored's opening "+
						"cannot hold both sides, so the total was kept and the bill-wise breakup dropped "+
						"(its ageing must be re-entered)", m.Name(), ref, drcrWord(dc), drcrWord(openDC)),
				im.masters.SetLedgerOpening(ctx, ledgerID, openBal, openDC, nil, createdBy)
		}
		bills = append(bills, masters.OpeningBill{
			RefName: ref,
			RefType: "NEW",
			DrCr:    dc,
			Amount:  amt.Abs(),
			DueDate: billDueISO(b.CreditPeriod),
		})
		sum = sum.Add(amt.Abs())
	}
	if len(bills) == 0 {
		// No usable breakup: still write the balance itself.
		return "", im.masters.SetLedgerOpening(ctx, ledgerID, openBal, openDC, nil, createdBy)
	}
	if !sum.Equal(openBal) {
		return fmt.Sprintf(
				"ledger %q: opening bill-wise breakup sums to %s but its opening balance is %s; the "+
					"total was kept and the breakup dropped (its ageing must be re-entered)",
				m.Name(), sum.StringFixed(2), openBal.StringFixed(2)),
			im.masters.SetLedgerOpening(ctx, ledgerID, openBal, openDC, nil, createdBy)
	}
	return "", im.masters.SetLedgerOpening(ctx, ledgerID, openBal, openDC, bills, createdBy)
}

// drcrWord spells a "D"/"C" for a message a human reads.
func drcrWord(dc string) string {
	if dc == "D" {
		return "debit"
	}
	return "credit"
}

// stateCodeFor determines a ledger's two-digit GST state code.
//
// ⛔ THE EXPORT DOES NOT CARRY ONE. Tally records the state by NAME
// ("Maharashtra"), in several places and sometimes in none of them: of the 342
// GST-registered parties in the file tested against, ZERO carried a two-digit
// code anywhere, so a reader that insisted on one — as this importer did —
// migrated every party with a blank state, and a blank state is what decides
// intra- vs inter-state on every invoice afterwards.
//
// The GSTIN is the best source and needs no table: its first two digits ARE the
// state code, by construction, and they agreed for 340 of those 342. It is
// validated against the statutory table so a retired code (25, 28) is refused
// rather than written into a master the invoice printer will later refuse to
// print. The state NAME is the fallback, and a literal two-digit field — which
// the hand-authored corpora do use — is honoured ahead of both.
func stateCodeFor(m *integration.TallyLedgerMaster) string {
	for _, s := range []string{m.State, m.StateName, m.LedgerStateName} {
		if s = strings.TrimSpace(s); len(s) == 2 {
			if _, ok := gststate.Name(s); ok {
				return s
			}
		}
	}
	if g := strings.TrimSpace(m.GSTINValue()); len(g) >= 2 {
		if code := g[:2]; code >= "01" && code <= "99" {
			if _, ok := gststate.Name(code); ok {
				return code
			}
		}
	}
	if code, ok := stateCodeByName(m.GSTStateName()); ok {
		return code
	}
	return ""
}

// stateCodeByName reverses the statutory state table: a Tally state NAME becomes
// the code cored stores. Only CURRENT entries answer — a ledger still naming
// "Daman and Diu" gets no code rather than a retired one.
func stateCodeByName(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", false
	}
	for _, e := range gststate.All() {
		if strings.EqualFold(e.Name, name) {
			if _, ok := gststate.Name(e.Code); ok {
				return e.Code, true
			}
			return "", false
		}
	}
	return "", false
}

// masterRegType maps a Tally GSTREGISTRATIONTYPE onto the cored enum. A blank or
// unknown value yields UNKNOWN; a GSTIN with an unspecified type is treated as
// REGULAR so the GSTR-1 join classifies it B2B.
func masterRegType(m *integration.TallyLedgerMaster) masters.GSTRegistrationType {
	switch strings.ToUpper(m.RegistrationTypeValue()) {
	case "REGULAR":
		return masters.GSTRegular
	case "COMPOSITION":
		return masters.GSTComposition
	case "CONSUMER":
		return masters.GSTConsumer
	case "UNREGISTERED":
		return masters.GSTUnregistered
	case "SEZ", "SPECIAL ECONOMIC ZONE":
		return masters.GSTSEZ
	default:
		if m.GSTINValue() != "" {
			return masters.GSTRegular
		}
		return masters.GSTUnknown
	}
}

// taxLedgerKind classifies a ledger name as a CGST/SGST/IGST tax line (or none),
// so a Sales voucher's GST lines can be separated from its taxable lines.
type taxLedgerKind int

const (
	notTax taxLedgerKind = iota
	taxCGST
	taxSGST
	taxIGST
)

// classifyTaxLedger inspects a ledger name for a GST head. IGST is checked first
// so a name containing "igst" is not mis-read as cgst/sgst.
func classifyTaxLedger(name string) taxLedgerKind {
	l := strings.ToLower(name)
	switch {
	case strings.Contains(l, "igst"):
		return taxIGST
	case strings.Contains(l, "cgst"):
		return taxCGST
	case strings.Contains(l, "sgst"):
		return taxSGST
	default:
		return notTax
	}
}

// importVoucher ensures the voucher's ledgers exist and posts it. It returns the
// post result and the list of ledger names it touched. For a Sales voucher whose
// entries include CGST/SGST/IGST tax lines, it builds ledger.GSTParams so the
// post path derives + verifies the tax against the engine and stamps the GST
// breakup on the version (which GSTR-1 then reads).
// billDueISO normalises a bill-allocation DUEDATE to an ISO YYYY-MM-DD string.
// Tally exports may already emit ISO (as our corpora do) or its own DD-Mon-YYYY
// form; an unparseable/empty value yields "" (no due date), which the ageing
// report treats as undated.
func billDueISO(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if _, err := time.Parse("2006-01-02", s); err == nil {
		return s // already ISO
	}
	if iso, err := integration.TallyDateToISO(s); err == nil {
		return iso
	}
	return ""
}

// buildVoucherInput assembles the ledger.VoucherInput for one Tally voucher
// (postings, GST params, bill-wise allocations) WITHOUT posting it — the caller
// dispatches to PostVoucher/AlterVoucher/DeleteVoucher based on the Tally ACTION.
func (im *Importer) buildVoucherInput(run cctx, idx int, v *integration.TallyVoucher,
	ledgerIDs map[string]uuid.UUID, ledgerState map[string]string, res *ImportResult) (ledger.VoucherInput, []string, error) {

	// The accounting half of this function predates the goods entirely and reads
	// `ctx` and `createdBy` throughout; they now travel together in `run` so the
	// stock half can reach the master cache. Bound here so the body below is
	// unchanged.
	ctx, createdBy := run.ctx, run.createdBy

	class, ok := classByVchType[strings.ToLower(strings.TrimSpace(v.VchType))]
	if !ok {
		// Fall back to the (possibly renamed) voucher-type name before giving up.
		class, ok = classByVchType[strings.ToLower(strings.TrimSpace(v.VoucherTypeName))]
	}
	if !ok {
		return ledger.VoucherInput{}, nil, fmt.Errorf(
			"migration: voucher %d: unrecognised VCHTYPE %q", idx+1, v.VchType)
	}

	entries := v.Entries()
	if len(entries) < 2 {
		return ledger.VoucherInput{}, nil, fmt.Errorf(
			"migration: voucher %d: needs at least two ledger entries, got %d", idx+1, len(entries))
	}

	isoDate, err := integration.TallyDateToISO(v.Date)
	if err != nil {
		return ledger.VoucherInput{}, nil, fmt.Errorf("migration: voucher %d: %w", idx+1, err)
	}

	party := strings.TrimSpace(v.PartyLedgerName)

	var (
		postings   []ledger.PostingInput
		billAllocs []ledger.BillAllocationInput
		names      []string
		// GST accumulation (Sales only): the taxable (non-tax, non-party) lines and
		// the tax-ledger ids + posted tax amounts.
		taxableLines []compliance.Line
		taxableTotal decimal.Decimal
		taxTotal     decimal.Decimal
		cgstID       uuid.UUID
		sgstID       uuid.UUID
		igstID       uuid.UUID
		partyID      uuid.UUID
	)
	for _, e := range entries {
		name := strings.TrimSpace(e.LedgerName)
		if name == "" {
			return ledger.VoucherInput{}, nil, fmt.Errorf("migration: voucher %d: empty ledger name", idx+1)
		}
		debit, err := e.Side()
		if err != nil {
			return ledger.VoucherInput{}, nil, fmt.Errorf("migration: voucher %d: %w", idx+1, err)
		}
		amount, err := e.Magnitude()
		if err != nil {
			return ledger.VoucherInput{}, nil, fmt.Errorf("migration: voucher %d: %w", idx+1, err)
		}

		drcr := ledger.Credit
		if debit {
			drcr = ledger.Debit
		}

		id, err := im.ensureLedger(ctx, name, class, drcr, ledgerIDs, createdBy)
		if err != nil {
			return ledger.VoucherInput{}, nil, fmt.Errorf("migration: voucher %d: ledger %q: %w", idx+1, name, err)
		}
		postings = append(postings, ledger.PostingInput{LedgerID: id, DrCr: drcr, Amount: amount})
		names = append(names, name)

		// Bill-wise allocations on this line (party ledger): each becomes a
		// BillAllocationInput on the SAME Dr/Cr side as the line, feeding the
		// receivables/payables ageing. A "New Ref" opens a bill; "Agst Ref"
		// settles one. Amount is magnitude (sign is carried by DrCr).
		for _, ba := range e.BillAllocations {
			ref := strings.TrimSpace(ba.Name)
			if ref == "" {
				continue
			}
			amt, err := decimal.NewFromString(strings.TrimSpace(ba.Amount))
			if err != nil {
				return ledger.VoucherInput{}, nil, fmt.Errorf(
					"migration: voucher %d: bill %q: bad AMOUNT %q: %w", idx+1, ref, ba.Amount, err)
			}
			billAllocs = append(billAllocs, ledger.BillAllocationInput{
				LedgerID: id,
				RefName:  ref,
				RefType:  ledger.RefType(ba.RefType()),
				DrCr:     drcr,
				Amount:   amt.Abs(),
				DueDate:  billDueISO(ba.DueDate),
			})
		}

		if name == party {
			partyID = id
		}

		if class == ledger.Sales {
			switch classifyTaxLedger(name) {
			case taxCGST:
				cgstID = id
				taxTotal = taxTotal.Add(amount)
			case taxSGST:
				sgstID = id
				taxTotal = taxTotal.Add(amount)
			case taxIGST:
				igstID = id
				taxTotal = taxTotal.Add(amount)
			default:
				// A non-tax line that is NOT the party line is a taxable (sales income)
				// line. The party line is the receivable, not taxable value.
				if name != party {
					taxableLines = append(taxableLines, compliance.Line{
						TaxableValue: amount,
						GSTRatePct:   decimal.Zero, // set after the loop from total tax/taxable
						HSN:          strings.TrimSpace(e.HSN),
					})
					taxableTotal = taxableTotal.Add(amount)
				}
			}
		}
	}

	// Resolve the voucher type for this class (the six are seeded per company).
	vtID, err := im.masters.VoucherTypeID(ctx, string(class))
	if err != nil {
		return ledger.VoucherInput{}, nil, fmt.Errorf(
			"migration: voucher %d: resolve voucher type %q: %w", idx+1, class, err)
	}

	in := ledger.VoucherInput{
		Class:           class,
		VoucherTypeID:   vtID,
		PartyLedgerID:   partyID,
		VoucherNo:       strings.TrimSpace(v.VoucherNumber),
		VoucherDate:     isoDate,
		Narration:       strings.TrimSpace(v.Narration),
		Party:           party,
		CreatedBy:       createdBy,
		Postings:        postings,
		BillAllocations: billAllocs,
		SourceGUID:      v.GUIDValue(),
	}

	// GST on Sales: when the voucher carried tax lines, attach GSTParams so the
	// post path derives + verifies the split. SupplierStateCode is left EMPTY on
	// purpose — ledger.post() fills it from the company's home state inside the tx
	// (WS3). The rate is inferred from total tax over taxable value (a whole
	// percent), unless an explicit GSTRATE was present on a taxable line.
	// THE GOODS. Independent of the postings, not derived from them: a 3-item
	// invoice billed to one Sales ledger is ONE posting and THREE movements, so
	// the two halves are built from two different parts of the file.
	stock, err := im.inventoryLinesFor(run, idx, v, class, res)
	if err != nil {
		return ledger.VoucherInput{}, nil, err
	}
	in.Inventory = stock

	if class == ledger.Sales && !taxTotal.IsZero() && !taxableTotal.IsZero() {
		rate := inferRate(v.Entries(), taxTotal, taxableTotal)
		for i := range taxableLines {
			taxableLines[i].GSTRatePct = rate
		}
		pos := strings.TrimSpace(v.PlaceOfSupply)
		if len(pos) != 2 {
			// Derive POS from the party ledger's declared home state.
			if st := ledgerState[party]; st != "" {
				pos = st
			}
		}
		in.GST = &ledger.GSTParams{
			PlaceOfSupplyStateCode: pos,
			ReverseCharge:          v.ReverseChargeFlag(),
			Lines:                  taxableLines,
			CGSTLedgerID:           cgstID,
			SGSTLedgerID:           sgstID,
			IGSTLedgerID:           igstID,
		}
	}

	return in, names, nil
}

// inferRate determines the combined GST rate percent for a Sales voucher. It
// prefers an explicit GSTRATE on any taxable line; otherwise it derives the rate
// as round(taxTotal*100/taxableTotal) to the nearest whole percent (GST slabs are
// integral: 0/5/12/18/28). See the rate-inference rounding risk in the spec.
func inferRate(entries []integration.TallyLedgerEntry, taxTotal, taxableTotal decimal.Decimal) decimal.Decimal {
	for _, e := range entries {
		if r := strings.TrimSpace(e.GSTRate); r != "" {
			if d, err := decimal.NewFromString(r); err == nil && !d.IsZero() {
				return d
			}
		}
	}
	if taxableTotal.IsZero() {
		return decimal.Zero
	}
	return taxTotal.Mul(decimal.NewFromInt(100)).Div(taxableTotal).Round(0)
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
	tb, err := rep.TrialBalance(ctx, companyID, "")
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
