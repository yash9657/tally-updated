// Package einvoice is cored's half of the e-invoice / IRN path: it assembles a
// rules.VoucherSnapshot — identity AND item list — from the books, hands it to
// statutoryd over HTTP, and reads back the IRN that statutoryd pinned onto the
// voucher.
//
// ===========================================================================
// ⛔⛔ WHY cored PROXIES INSTEAD OF THE BROWSER CALLING statutoryd DIRECTLY
// ===========================================================================
// The decision and its argument are HANDOFF §1.4 B.2, and the argument is a
// fact about the data rather than a preference:
//
//   - The NIC e-invoice schema requires AT LEAST ONE ITEM.
//   - statutoryd's HTTPCoredClient.GetVoucher maps exactly five amount fields
//     off cored's voucher JSON — VoucherNo, VoucherDate, Taxable, CGST/SGST/IGST
//     — and NO LINES. A request that supplies none is refused with
//     `[ItemList: 0 items, minimum 1]`.
//   - Only cored holds the inventory rows, the stock masters, the HSN and the
//     rate. A browser could not assemble the list either; it would have to fetch
//     every piece separately and re-derive the tax split by hand.
//
// So the payload can only be built by something holding cored's masters, and
// that settles the trigger too. The proxy additionally keeps the whole path
// inside cored's PDP, RLS, step-up and company scoping, none of which statutoryd
// has. The price is a hard runtime dependency on a second process; it is paid
// explicitly in statutoryd_client.go's ErrStatutorydUnreachable rather than
// passed to the browser as a bare 502.
//
// ===========================================================================
// ⛔⛔ THE RE-ENTRANCY RULE. READ IT BEFORE CHANGING Raise.
// ===========================================================================
// The call graph is cored -> statutoryd -> cored. While Raise is waiting,
// statutoryd calls BACK into cored twice: GET the voucher, then POST
// /vouchers/{id}/external-reference to pin the IRN. THE HANDLER MUST THEREFORE
// NOT HOLD A DATABASE TRANSACTION ACROSS THE CALL. A held transaction is a
// connection taken out of this process's own pool and not given back until the
// callback it is waiting for completes — so the callback cannot get one, and the
// failure mode is a HUNG REQUEST UNDER CONCURRENCY, not an error in a test.
//
// Raise is therefore two phases with a commit between them, and the phases are
// separated by a comment banner rather than left to be inferred.
//
// ===========================================================================
// ⛔ WHAT THIS PACKAGE MAY NOT DO: DECIDE A STATUTORY QUESTION
// ===========================================================================
// It assembles facts and it refuses the cases it cannot describe. It does not
// validate the payload (rules/statutory does), it does not classify a provider
// failure (statutoryd does), and it does not re-derive one paisa of tax: the
// per-line HSN, taxable value, rate and CGST/SGST/IGST split are the voucher's
// OWN voucher_gst_line rows, which are written from the same TaxResult that
// produced the aggregate. Re-deriving them here would be a second tax engine and
// the first divergence would be an e-invoice disagreeing with the GSTR-1 filed
// from the same document.
package einvoice

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/statutory/rules"
)

// ErrNotFound is returned when the voucher does not exist in this company, or
// when no IRN has been raised against it. A sentinel so the HTTP layer answers
// 404 rather than 500.
var ErrNotFound = errors.New("einvoice: no such voucher, or no e-invoice has been raised for it in this company")

// ErrNoTaxLines refuses a voucher that carries no per-line GST detail.
//
// ⛔ AN e-INVOICE IS A TAX DOCUMENT AND ITS ItemList IS THE TAX BREAKDOWN. A
// voucher with no voucher_gst_line rows is a receipt, a payment, a contra, a
// journal or a delivery note — none of which is an invoice, and NIC's
// minimum-one-item rule says the same thing from the other end. Inventing a
// single line from the voucher aggregate would produce a document that validates
// and describes nothing.
var ErrNoTaxLines = errors.New(
	"einvoice: this voucher carries no per-line GST detail, so it has no invoice item list — " +
		"an e-invoice is a tax document and NIC requires at least one item. Raise it against a " +
		"TAX INVOICE (a sales voucher with a GST block), not a receipt, payment, contra, journal " +
		"or delivery note")

// ErrNotAnOutwardSalesInvoice refuses every voucher class that is not an outward
// SALES invoice, BY NAME.
//
// ===========================================================================
// ⛔⛔ THIS SENTINEL EXISTS BECAUSE A PURCHASE COULD BE e-INVOICED, AND WAS THEN
// FROZEN FOREVER
// ===========================================================================
// loadInvoice used to read no voucher type at all. It refused receipts,
// journals and service invoices — but only as a SIDE EFFECT of their having no
// GST lines or no goods. Anything carrying both GST lines and stock lines was
// accepted, and that set includes PURCHASE, CREDIT NOTE and DEBIT NOTE.
//
// A purchase raised this way produced a document asserting that the company SOLD
// goods to its own supplier: the seller GSTIN is the company's own (it comes off
// the `company` row) and the buyer GSTIN is the vendor's. The freeze hook then
// made a legitimate purchase record un-alterable and un-deletable, and there is
// no undo anywhere in this product.
//
// ⛔ AND IT COLLIDES AT THE IRP. NIC derives IRN = hash(SupplierGSTIN, DocTyp,
// DocNo, FY) and BuildEInvoice sends DocTyp "INV" with the voucher's own number.
// Numbering here is PER CLASS, so Sales #1 and Purchase #1 are one NIC key: the
// second is rejected as a duplicate. FakeProvider hashes the whole payload and
// mints two different IRNs, so no test in this stack could ever have shown it.
//
// ⚠️ CREDIT AND DEBIT NOTES ARE LEGITIMATELY e-INVOICEABLE IN INDIA, as CRN and
// DBN, and refusing them is a real gap rather than a rule. Emitting one means
// modelling the document type AND the reference to the original invoice that NIC
// requires alongside it; this build models neither, and a credit note submitted
// as "INV" is a false document with a government reference number on it. So they
// are refused by name, with the gap stated, exactly as ErrLineNotDescribable
// states the service-invoice gap.
var ErrNotAnOutwardSalesInvoice = errors.New(
	"einvoice: an e-invoice is the OUTWARD document a seller issues, so it can only be raised " +
		"against a SALES invoice. Supported: Sales (NIC document type INV). NOT supported: " +
		"Purchase, Credit Note and Debit Note — a purchase e-invoiced from these books would " +
		"declare that this company SOLD the goods to its own supplier, and a credit or debit " +
		"note is a different NIC document type (CRN/DBN, with a reference to the original " +
		"invoice) that this version does not build. Nothing else — receipt, payment, contra, " +
		"journal, delivery note, payroll — is an invoice at all")

// ErrNoUnitOfMeasure refuses a line whose stock master carries no unit.
//
// ⛔ NIC's ItemList[].Unit IS MANDATORY FOR GOODS AND WAS SHIPPING BLANK on every
// line this package ever built — `COALESCE(u.symbol, ”)` turned "the master does
// not say" into an empty string, ValidateEInvoice never looked at the field, and
// the fixture the whole test suite is built on is an item with no unit. So a
// blank was minted and frozen without a word.
//
// ⛔ AND THE FIX IS A REFUSAL, NOT A DEFAULT. "NOS" is the tempting default and it
// is a claim about the goods: ten NOS of something sold by the kilogram is a
// different consignment from ten kilograms, and the difference is invisible on
// the face of the document. It is the same argument ErrLineNotDescribable makes
// about a guessed description, and it gets the same answer — this build will not
// put a guess on a document that carries a government reference number and then
// freeze the voucher against it. Setting the unit on the item master is a
// one-field edit; un-freezing a voucher is impossible.
var ErrNoUnitOfMeasure = errors.New(
	"einvoice: this invoice's goods have no unit of measure on their stock master, and NIC " +
		"requires one on every item line. Set the unit on the stock item (`unit_symbol`, e.g. " +
		"NOS, KGS, MTR) and raise the e-invoice again — this build will not guess a unit onto a " +
		"document that carries a government reference number")

// ErrPrecisionNotExpressible refuses a line whose value cannot be stated in
// NIC's two-decimal item block without contradicting the books.
//
// ⛔ IT IS NOT A ROUNDING BUG AND IT CANNOT BE ROUNDED AWAY. NIC's item block
// asserts BOTH `TotAmt = Qty x UnitPrice` AND `AssAmt = TotAmt - Discount`, all
// at two decimals. The books keep four, and `voucher_gst_line.taxable_value` —
// which is the figure GSTR-1 is filed on — has already been rounded to four by
// Postgres before this package sees it. When the exact product sits on a .xx5
// boundary, the four-place figure and the two-place product round in OPPOSITE
// directions and the two identities cannot both hold:
//
//	qty 0.0011 x rate 10913.6000 = 12.00496 exactly
//	  books:  round4 -> 12.0050  -> AssAmt 12.01
//	  NIC:    round2 of the product -> TotAmt 12.00
//	  => Discount -0.01, and NIC has no surcharge field.
//
// ⛔ THE THREE ALTERNATIVES ARE ALL WORSE, AND THAT IS THE ARGUMENT.
// Emitting the negative discount ships a document the IRP rejects. Restating
// AssAmt as the gross files a taxable value that is not the one in the books.
// Clamping the discount at zero produces a line whose own three numbers do not
// foot. Each of those puts a wrong number on a document that carries a
// government reference number and then FREEZES the voucher against it, with no
// undo anywhere in the product. A refusal that names the line and both figures
// is the only option that does not lie (HANDOFF §9.6).
//
// ⚠️ IT IS RARE AND IT IS REAL: it needs a quantity and rate whose product lands
// exactly on a half-paisa, which sub-unit quantities of high-value goods
// (metals, chemicals) genuinely produce. The operator's remedy is to state the
// line at a precision that closes — and the message gives them both figures so
// they can see which way it went.
var ErrPrecisionNotExpressible = errors.New(
	"einvoice: this line's value cannot be stated in NIC's two-decimal item block without " +
		"contradicting the books — the gross and the taxable value round in opposite " +
		"directions, so the document would have to declare a negative discount")

// ErrLineMismatch refuses a voucher whose stock lines and GST lines do not
// correspond one-for-one.
//
// ⛔ IT REFUSES RATHER THAN PAIRING BY POSITION AND HOPING, for exactly the
// reason internal/ewaybill's identically-named sentinel does. On an item invoice
// the server GENERATES the GST lines from the stock lines at the same index
// (ledger/item_invoice.go), so line N of one IS line N of the other and the
// correspondence is structural. This package DEPENDS on that and therefore
// checks it: if the two ever stop corresponding, pairing by position would put
// one product's HSN and tax rate against another product's name and quantity —
// an e-invoice that is internally consistent, passes every schema check, and is
// wrong about what was sold. That is an IRN minted against a false document, and
// the freeze hook then pins the voucher to it permanently.
//
// ===========================================================================
// ⚠️⚠️ IT IS UNREACHABLE THROUGH EVERY PUBLIC PATH TODAY, AND IT IS KEPT ANYWAY.
// STATED HERE SO THE NEXT READER DOES NOT HAVE TO RE-DERIVE IT.
// ===========================================================================
// A review confirmed no request can reach it. The enumeration, which is the part
// worth keeping:
//
//   - GST lines AND stock lines, generated together: only ledger's item-invoice
//     generator writes both, and it writes exactly one GST line per stock line
//     from the same loop.
//   - GST lines supplied by the client ALONGSIDE inventory: refused one layer up
//     by ledger.ErrInventoryAndGSTLines, before anything is written.
//   - Stock lines with no GST lines (delivery note, receipt note, un-taxed
//     purchase): gstCount == 0, and the check above this one answers
//     ErrNoTaxLines first, deliberately.
//   - GST lines with no stock lines (a service invoice): every row scans a NULL
//     name and ErrLineNotDescribable fires inside the loop.
//   - len(out) != gstCount is impossible by construction: the query's spine IS
//     voucher_gst_line and the joins are LEFT.
//
// ⛔ SO IT IS A BACKSTOP AGAINST A FUTURE WRITER — an importer, the offline
// sequencer, a migration, a second generator — not against today's callers, and
// it is kept for the same reason migration 0027's deferred trigger is kept even
// though the Go code cannot violate it either. What it guards is not a coding
// slip but a SILENT one: pairing by position across a mismatch produces a
// document that foots, validates, mints an IRN and describes a sale that did not
// happen. If you delete the check, delete this comment with it — and do not
// delete only the sentinel, leaving the condition to fall through to a nil
// error.
var ErrLineMismatch = errors.New(
	"einvoice: this voucher's stock lines and GST lines do not correspond one-for-one, " +
		"so the goods cannot be matched to their tax rates")

// ErrLineNotDescribable refuses a taxed line the books cannot describe.
//
// ⛔ THIS IS THE SERVICE-INVOICE GAP, NAMED RATHER THAN PAPERED OVER, and it is
// a real workflow that this build does not cover. voucher_gst_line (migration
// 0016) records an HSN/SAC, a taxable value and a rate — and NO DESCRIPTION, NO
// QUANTITY AND NO UNIT, because it exists to feed the GSTR-1 table-12 rollup,
// which keys on (hsn, rate) and needs none of those. For an ITEM invoice the
// description comes from the stock master via stock_movement. For a SERVICE
// invoice there is no stock movement and nothing in the books names the line at
// all.
//
// ⚠️ THE TEMPTING FIX IS WRONG. The nearest available string is the posting's
// ledger name ("Consultancy Fees"), and pairing GST line N to posting N is NOT
// structural — the postings carry the party and the tax ledgers too. Getting it
// wrong writes one service's name against another's HSN and value onto a
// document with a government reference number on it, and the freeze hook makes
// the voucher un-alterable afterwards. A refusal an operator can read beats a
// statutory document nobody can detect is false.
var ErrLineNotDescribable = errors.New(
	"einvoice: this voucher's taxed lines carry no goods, so the e-invoice item list cannot be " +
		"described — the books record an HSN/SAC, a value and a rate for each line but no " +
		"description, quantity or unit, and every NIC item needs one. Raise the e-invoice from an " +
		"ITEM invoice (a sales voucher with inventory lines). Service invoices are a real workflow " +
		"and are NOT built in this version: this build will not put a blank or guessed description " +
		"on a document that carries a government reference number")

// TxRunner is the minimal database capability this package needs — the same
// seam internal/ewaybill and internal/orders take, and for the same reason: the
// RLS GUC is set per transaction, so a package that holds a pool can start its
// own inside the caller's scope.
type TxRunner interface {
	RunInTx(ctx context.Context, fn db.TxFunc) error
}

// Service raises e-invoices against the books and reads the pinned IRN back.
type Service struct {
	runner     TxRunner
	statutoryd StatutorydClient
}

// NewService constructs an e-invoice Service over a transaction runner and a
// statutoryd client.
func NewService(runner TxRunner, statutoryd StatutorydClient) *Service {
	return &Service{runner: runner, statutoryd: statutoryd}
}

// Input is one raise request. It is deliberately tiny: everything an e-invoice
// says is already in the books, which is the entire argument for the proxy.
type Input struct {
	VoucherID uuid.UUID
}

// Result is a raised e-invoice as it reaches the wire.
type Result struct {
	VoucherID uuid.UUID
	VoucherNo string

	IRN    string
	AckNo  string
	Status string

	// PinnedVoucherNo is the display number the freeze hook FROZE onto the
	// voucher. It is not decoration: from this moment the voucher cannot be
	// altered or deleted, and this is the number the IRN refers to.
	PinnedVoucherNo string
}

// Ref is a stored IRN read back off a voucher.
//
// ⚠️ IT CARRIES NO ACK NUMBER, AND THAT IS THE SCHEMA'S DOING RATHER THAN AN
// OMISSION HERE. voucher_external_reference (migration 0009) holds the kind, the
// ref value and the pinned number; there is no column for the NIC
// acknowledgement number or its timestamp. The ack number is therefore available
// on the RAISE and never again — a screen that wants to show it must show it
// then.
type Ref struct {
	VoucherID       uuid.UUID
	VoucherNo       string
	IRN             string
	PinnedVoucherNo string
	CreatedBy       string
	CreatedAt       time.Time
}

// Raise builds the e-invoice snapshot from the books and asks statutoryd to mint
// an IRN for it.
//
// ⛔⛔ TWO PHASES, WITH A COMMIT BETWEEN THEM. See the package comment: the call
// graph is cored -> statutoryd -> cored, so no transaction may be open while
// statutoryd is running.
func (s *Service) Raise(ctx context.Context, in Input) (Result, error) {
	if in.VoucherID == uuid.Nil {
		// ⛔ The all-zeros UUID PARSES (HANDOFF §9.5), so an explicit
		// 00000000-… reaches here as a valid uuid. Refuse it rather than letting
		// it become a lookup that finds nothing and reads as "that voucher is
		// gone".
		return Result{}, ErrNotFound
	}
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return Result{}, db.ErrNoCompany
	}

	// ---- PHASE 1: READ THE BOOKS. This transaction COMMITS before phase 2.
	var snap rules.VoucherSnapshot
	if err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		snap, err = loadInvoice(ctx, tx, in.VoucherID)
		return err
	}); err != nil {
		return Result{}, err
	}

	// ---- PHASE 2: NO TRANSACTION IS OPEN. statutoryd will now call BACK into
	// this process — GET the voucher, then POST the freeze hook — and those
	// callbacks need connections from the very pool phase 1 just released.
	res, err := s.statutoryd.RaiseEInvoice(ctx, companyID.String(), in.VoucherID.String(), rules.EInvoiceRequest{
		SellerGstin:     snap.SellerGstin,
		SellerLegalName: snap.SellerLegalName,
		SellerStateCode: snap.SellerStateCode,
		BuyerGstin:      snap.BuyerGstin,
		BuyerLegalName:  snap.BuyerLegalName,
		BuyerStateCode:  snap.BuyerStateCode,
		Lines:           snap.Lines,
		Taxable:         snap.Taxable,
		CGST:            snap.CGST,
		SGST:            snap.SGST,
		IGST:            snap.IGST,
		InvoiceTotal:    snap.InvoiceTotal,
	})
	if err != nil {
		return Result{}, err
	}

	return Result{
		VoucherID:       in.VoucherID,
		VoucherNo:       snap.VoucherNo,
		IRN:             res.IRN,
		AckNo:           res.AckNo,
		Status:          res.Status,
		PinnedVoucherNo: res.PinnedVoucherNo,
	}, nil
}

// Get reads the IRN pinned onto one voucher. ErrNotFound covers both "no such
// voucher" and "no IRN raised", which is the right conflation for a reader: in
// neither case is there an e-invoice to show, and distinguishing them would tell
// an unauthorized caller which voucher ids exist.
func (s *Service) Get(ctx context.Context, voucherID uuid.UUID) (Ref, error) {
	if voucherID == uuid.Nil {
		return Ref{}, ErrNotFound
	}
	var out Ref
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// The pin is keyed on (company, voucher, kind, ref) and a voucher could in
		// principle hold more than one IRN row; ORDER BY created_at takes the
		// FIRST one raised, which is the one the freeze pinned the number to.
		err := tx.QueryRow(ctx, `
			SELECT x.ref_value, x.pinned_voucher_no, COALESCE(x.created_by, ''), x.created_at,
			       COALESCE(vv.voucher_no, '')
			  FROM voucher_external_reference x
			  JOIN voucher_head vh    ON vh.voucher_id = x.voucher_id
			  JOIN voucher_version vv ON vv.id = vh.current_version_id
			 WHERE x.voucher_id = $1 AND x.kind = 'IRN' AND NOT vh.is_deleted
			 ORDER BY x.created_at ASC
			 LIMIT 1`, voucherID).Scan(
			&out.IRN, &out.PinnedVoucherNo, &out.CreatedBy, &out.CreatedAt, &out.VoucherNo)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("einvoice: read the pinned IRN: %w", err)
		}
		out.VoucherID = voucherID
		return nil
	})
	if err != nil {
		return Ref{}, err
	}
	return out, nil
}

// loadInvoice assembles the e-invoice snapshot from the books: the document
// identity, the seller from the `company` row, the buyer from the party `ledger`
// row, and the item list from voucher_gst_line joined to the stock movements.
//
// It is a DELIBERATE COUSIN of internal/ewaybill's loadConsignment (HANDOFF
// §1.4 B.4 says to copy that query) and it differs in three places that matter.
// The differences are listed here rather than left to a reader diffing two
// files, because each one is a bug if it is copied across:
//
//  1. THE SPINE IS voucher_gst_line, NOT stock_movement. An e-invoice is a tax
//     document: every TAXED line must appear on it. An e-way bill is a movement
//     document, so its spine is the goods.
//  2. THE BUYER'S STATE CODE IS THE VOUCHER'S place_of_supply, NOT THE LEDGER'S
//     state_code. See the block at that line — using the ledger's would make the
//     schema check disagree with the tax the voucher actually carries.
//  3. THE MONEY IS ROUNDED TO TWO PLACES HERE, not left at four for the payload
//     builder to round (see roundMoney) — AND THE INVOICE FIGURES ARE THE SUM OF
//     THE ROUNDED LINES rather than the voucher's own aggregate columns, which is
//     a one-paisa difference the real IRP rejects. The money block below has the
//     whole argument; the e-way bill has no item list and no equivalent.
func loadInvoice(ctx context.Context, tx pgx.Tx, voucherID uuid.UUID) (rules.VoucherSnapshot, error) {
	var (
		versionID   uuid.UUID
		voucherNo   string
		vDate       string
		pos         string
		partyID     *uuid.UUID
		sellerGst   string
		sellerName  string
		sellerSt    string
		parentClass string
		typeName    string
	)

	// ⚠️ THE AGGREGATE GST COLUMNS (vv.taxable_value / cgst / sgst / igst) ARE
	// DELIBERATELY NOT SELECTED. They used to be, and using them was the one-paisa
	// defect described at the money block below: the payload's figures come from
	// the ITEM LIST, so reading the aggregate here would only offer a second,
	// subtly different set of numbers for a later edit to reach for.
	//
	// ⛔⛔ vt.parent_class IS SELECTED AND IT IS THE FIRST THING CHECKED. Its
	// absence is what let a PURCHASE be e-invoiced and then frozen — see
	// ErrNotAnOutwardSalesInvoice. The join is LEFT and the value COALESCEd
	// because voucher_version.voucher_type_id is NULLABLE and really does go NULL
	// (migrations 0029 and 0034 both document the case); an INNER JOIN would
	// answer "no such voucher" for a voucher that exists, which is the wrong
	// refusal, and a NULL must fail CLOSED rather than fall through the class
	// gate.
	err := tx.QueryRow(ctx, `
		SELECT vh.current_version_id,
		       COALESCE(vv.voucher_no, ''),
		       COALESCE(to_char(vv.voucher_date, 'YYYY-MM-DD'), ''),
		       COALESCE(vv.place_of_supply, ''),
		       vv.party_ledger_id,
		       COALESCE(c.gstin, ''), COALESCE(NULLIF(c.mailing_name, ''), c.name),
		       COALESCE(c.state_code, ''),
		       COALESCE(vt.parent_class, ''), COALESCE(vt.name, '')
		  FROM voucher_head vh
		  JOIN voucher_version vv ON vv.id = vh.current_version_id
		  JOIN company c ON c.id = vh.company_id
		  LEFT JOIN voucher_type vt ON vt.id = vv.voucher_type_id
		 WHERE vh.voucher_id = $1 AND NOT vh.is_deleted`, voucherID).Scan(
		&versionID, &voucherNo, &vDate, &pos,
		&partyID, &sellerGst, &sellerName, &sellerSt,
		&parentClass, &typeName)
	if errors.Is(err, pgx.ErrNoRows) {
		return rules.VoucherSnapshot{}, ErrNotFound
	}
	if err != nil {
		return rules.VoucherSnapshot{}, fmt.Errorf("einvoice: load voucher: %w", err)
	}

	// ⛔⛔ THE CLASS GATE, AND IT RUNS BEFORE ANY LINE IS READ.
	//
	// It is first because it is the most decisive fact about the document and
	// because the alternative refusals describe the wrong thing: a Purchase with
	// a proper GST block and proper goods has nothing wrong with its LINES, and
	// refusing it for a line reason (or accepting it, which is what happened)
	// names a defect that is not there. A refusal that says "this is a Purchase
	// and only a Sales invoice can be e-invoiced" is one an operator can act on.
	if parentClass != salesParentClass {
		return rules.VoucherSnapshot{}, fmt.Errorf("%w. This voucher is %s",
			ErrNotAnOutwardSalesInvoice, describeClass(typeName, parentClass))
	}

	var buyerGst, buyerName, buyerSt string
	if partyID != nil {
		err = tx.QueryRow(ctx, `
			SELECT COALESCE(gstin, ''), name, COALESCE(state_code, '')
			  FROM ledger WHERE id = $1`, *partyID).Scan(&buyerGst, &buyerName, &buyerSt)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return rules.VoucherSnapshot{}, fmt.Errorf("einvoice: load party: %w", err)
		}
	}

	// ⛔⛔ THE BUYER'S STATE CODE IS THE VOUCHER'S PLACE OF SUPPLY, AND COPYING
	// ewaybill's `buyerSt` HERE WOULD BE A BUG.
	//
	// BuildEInvoice puts VoucherSnapshot.BuyerStateCode into BOTH BuyerDtls.Pos
	// and BuyerDtls.Stcd, and ValidateEInvoice then decides intra- vs inter-State
	// by comparing SellerDtls.Stcd against BuyerDtls.Pos — and cross-checks that
	// decision against the CGST/SGST/IGST split.
	//
	// That split was derived from voucher_version.place_of_supply when the
	// voucher was posted. The party ledger's state_code is a MASTER-DATA fact
	// about where the customer is registered and can legitimately differ (a
	// bill-to/ship-to sale, or a master corrected after the invoice was raised).
	// Feed the ledger's value in and the validator compares the wrong pair: a
	// perfectly good intra-State invoice is refused as "inter-state supply but
	// CgstVal/SgstVal is non-zero", naming a defect in the tax rather than in the
	// field this function chose.
	//
	// ⚠️ AND NOTE THE COLLAPSE THIS INHERITS. Pos and Stcd are ONE field in the
	// shared snapshot, so on a bill-to/ship-to invoice the buyer's registered
	// state is reported as the place of supply. NIC models that case with a
	// separate ShipDtls block, which this build does not emit. The place of
	// supply is the one of the two the tax depends on, so it wins.
	//
	// The ledger's state code remains the fallback for a voucher that recorded no
	// place of supply at all (a non-GST voucher), where there is nothing better.
	if pos != "" {
		buyerSt = pos
	}

	lines, lineSum, err := loadLines(ctx, tx, versionID)
	if err != nil {
		return rules.VoucherSnapshot{}, err
	}

	// ⛔⛔ THE INVOICE FIGURES ARE THE SUM OF THE ROUNDED LINES, NOT THE ROUNDED
	// AGGREGATE — AND THE DIFFERENCE IS ONE PAISA THAT A REAL IRP REJECTS.
	//
	// The books hold NUMERIC(19,4) and NIC prints two places, so something must
	// round. There are two places to do it and THEY DO NOT AGREE:
	//
	//	round(SUM(line taxables))   -- voucher_version.taxable_value, rounded here
	//	SUM(round(line taxable))    -- what the ITEM LIST on the document adds to
	//
	// 3 units at 10.0050 is 30.0150 on a line. Two such lines: each rounds to
	// 30.02, so the item list sums to 60.04, while the aggregate 60.0300 rounds
	// to 60.03. Taking the aggregate would put an AssVal on the document that its
	// own ItemList does not add up to.
	//
	// ⛔ AND NOTHING IN THIS STACK CATCHES THAT. ValidateEInvoice checks the
	// footing TotInvVal == AssVal + CgstVal + SgstVal + IgstVal — which still
	// holds, because all four would be aggregates — and never checks the item
	// list against AssVal. The REAL IRP does ("sum of AssAmt of all items must
	// equal AssVal"), so the fake provider mints an IRN for a payload NIC would
	// refuse, and the failure surfaces only against production credentials.
	// Pinned by TestTheItemListFootsToTheInvoiceWhenTheBOOKSCARRYFOURDECIMALS.
	//
	// So the ITEM LIST IS THE AUTHORITY for this document and the invoice figures
	// are derived from it. The footing stays exact by construction for the same
	// reason it did before — the total is the sum of the four figures actually
	// printed.
	//
	// ⚠️ THE CONSEQUENCE, STATED RATHER THAN HIDDEN: a two-place statutory
	// document can now differ from the four-place books by less than a paisa per
	// line. That is inherent to the format — SOME rounding was always going to
	// happen — and it is not a tolerance anywhere in the reconciliation: the
	// document is exact against itself, and the books remain exact against
	// themselves at four places.
	//
	// ⚠️ cgst/sgst/igst are unchanged in practice and are summed anyway. TaxDerive
	// already rounds each line's tax to the paisa and the version's aggregate is
	// the sum of exactly those, so the two agree today; summing the lines makes
	// that AGREEMENT STRUCTURAL instead of a fact about TaxDerive that this file
	// would silently depend on.
	taxable2 := lineSum.taxable
	cgst2, sgst2, igst2 := lineSum.cgst, lineSum.sgst, lineSum.igst
	total2 := taxable2.Add(cgst2).Add(sgst2).Add(igst2)

	return rules.VoucherSnapshot{
		VoucherNo:       voucherNo,
		VoucherDate:     vDate,
		SellerGstin:     sellerGst,
		SellerLegalName: sellerName,
		SellerStateCode: sellerSt,
		BuyerGstin:      buyerGst,
		BuyerLegalName:  buyerName,
		BuyerStateCode:  buyerSt,
		Lines:           lines,
		Taxable:         taxable2.StringFixed(2),
		CGST:            cgst2.StringFixed(2),
		SGST:            sgst2.StringFixed(2),
		IGST:            igst2.StringFixed(2),
		InvoiceTotal:    total2.StringFixed(2),
	}, nil
}

// salesParentClass is the ONE voucher_type.parent_class an e-invoice may be
// raised against. It is the upper-cased, underscored form migration 0005 seeds
// and internal/ledger's numbering resolves against
// (`parent_class = upper(replace($2, ' ', '_'))`), so a company's own custom
// SALES-class voucher type ("Retail Sales", "Export Sales") is accepted while a
// type merely NAMED "Sales" under another class is not — the class is the
// behaviour, the name is a label.
const salesParentClass = "SALES"

// describeClass renders a voucher's type for a refusal message.
//
// ⛔ IT NAMES THE TYPE THE OPERATOR SEES ("Credit Note") AND THE CLASS THE SYSTEM
// ROUTES ON ("CREDIT_NOTE"), because on a company with custom types those two
// differ and a message carrying only one of them cannot be matched to what is on
// the screen. A voucher with no type row at all is named as such rather than
// described as some class it does not have.
func describeClass(typeName, parentClass string) string {
	switch {
	case typeName != "" && parentClass != "":
		return fmt.Sprintf("a %s (voucher class %s)", typeName, parentClass)
	case parentClass != "":
		return fmt.Sprintf("of voucher class %s", parentClass)
	default:
		return "of no recorded voucher type at all (voucher_version.voucher_type_id is NULL), " +
			"so its class cannot be established and it is refused rather than assumed to be a sale"
	}
}

// lineTotals is the item list's own footing: the sum of the values ACTUALLY
// PRINTED on the lines, at the precision they are printed to.
//
// ⛔ IT IS RETURNED RATHER THAN RE-DERIVED BY THE CALLER, and that is the point.
// The invoice-level AssVal/CgstVal/SgstVal/IgstVal must equal the item list or
// the IRP refuses the document; making the caller add the lines up again from
// their strings would be a second implementation of the same sum, which is the
// arrangement this file exists to avoid one level up (see rules/einvoice_wire.go
// on two definitions of one contract).
type lineTotals struct {
	taxable decimal.Decimal
	cgst    decimal.Decimal
	sgst    decimal.Decimal
	igst    decimal.Decimal
}

// loadLines builds the NIC item list and its footing.
//
// ⛔ THE SPINE IS voucher_gst_line AND THE GOODS ARE JOINED ONTO IT, not the
// other way round. An e-invoice must account for every TAXED line; a stock
// movement with no tax line contributes nothing to the invoice value and would
// be describing goods the document does not charge for. The counts are still
// checked on both sides, because a LEFT JOIN silently tolerates exactly the
// disagreement ErrLineMismatch exists to refuse.
func loadLines(ctx context.Context, tx pgx.Tx, versionID uuid.UUID) ([]rules.SnapshotLine, lineTotals, error) {
	// ⚠️ u.symbol IS *NOT* COALESCEd, AND THAT IS THE FIX FOR A BLANK MANDATORY
	// FIELD. `COALESCE(u.symbol, '')` turned "this item master names no unit"
	// into an empty string that scanned cleanly, passed every check and was
	// frozen onto the document. A NULL now scans into a *string and is refused by
	// name (ErrNoUnitOfMeasure).
	//
	// ⚠️ m.discount_pct IS DELIBERATELY *NOT* SELECTED, EVEN THOUGH THIS FILE NOW
	// EMITS A DISCOUNT. The discount that goes on the document is the difference
	// between the gross (qty x rate) and the taxable value the books already
	// derived — see the block at gross2 below. Re-applying the PERCENTAGE here
	// would be a second discount engine beside ledger.InventoryLine.TaxableValue,
	// and the two would disagree by a paisa on lines like 3 x 333.33 @ 7.5%
	// exactly as that method's own comment warns. One derivation, read here as a
	// difference.
	rows, err := tx.Query(ctx, `
		SELECT g.line_no, COALESCE(g.hsn, ''), g.taxable_value, g.gst_rate_pct,
		       g.cgst, g.sgst, g.igst,
		       si.name, u.symbol, m.qty, m.rate
		  FROM voucher_gst_line g
		  LEFT JOIN stock_movement m
		         ON m.voucher_version_id = g.voucher_version_id AND m.line_no = g.line_no
		  LEFT JOIN stock_item si ON si.id = m.stock_item_id
		  LEFT JOIN unit u        ON u.id = si.unit_id
		 WHERE g.voucher_version_id = $1
		 ORDER BY g.line_no`, versionID)
	if err != nil {
		return nil, lineTotals{}, fmt.Errorf("einvoice: load invoice lines: %w", err)
	}
	defer rows.Close()

	var out []rules.SnapshotLine
	sum := lineTotals{taxable: decimal.Zero, cgst: decimal.Zero, sgst: decimal.Zero, igst: decimal.Zero}
	for rows.Next() {
		var (
			lineNo                       int
			hsn                          string
			unit, name                   *string
			qty, rate                    *decimal.Decimal
			lineTaxable, ratePct         decimal.Decimal
			lineCGST, lineSGST, lineIGST decimal.Decimal
		)
		if err := rows.Scan(&lineNo, &hsn, &lineTaxable, &ratePct,
			&lineCGST, &lineSGST, &lineIGST, &name, &unit, &qty, &rate); err != nil {
			return nil, lineTotals{}, fmt.Errorf("einvoice: scan invoice line: %w", err)
		}
		if name == nil || qty == nil || rate == nil {
			// A taxed line with no goods behind it. See ErrLineNotDescribable: the
			// books hold no description for it and this build will not invent one.
			return nil, lineTotals{}, fmt.Errorf("%w (line %d)", ErrLineNotDescribable, lineNo)
		}
		// ⛔ NIC's Unit IS MANDATORY FOR GOODS. A master with no unit is refused
		// rather than defaulted; see ErrNoUnitOfMeasure for why "NOS" is a claim
		// and not a default.
		if unit == nil || strings.TrimSpace(*unit) == "" {
			return nil, lineTotals{}, fmt.Errorf("%w (line %d, %s)", ErrNoUnitOfMeasure, lineNo, *name)
		}

		// Same rounding rule as the invoice totals, one level down, so an item's
		// own TotItemVal foots to its parts at the precision NIC prints.
		taxable2 := roundMoney(lineTaxable)
		cgst2, sgst2, igst2 := roundMoney(lineCGST), roundMoney(lineSGST), roundMoney(lineIGST)

		// ⛔⛔ THE DISCOUNT IS THE DIFFERENCE ACTUALLY APPLIED, NOT A RE-DERIVED
		// PERCENTAGE.
		//
		// NIC's item block asserts TotAmt = Qty x UnitPrice and
		// AssAmt = TotAmt - Discount. The books hold the OTHER two of those four
		// numbers: the gross rate (stock_movement.rate) and the taxable value that
		// ledger.InventoryLine.TaxableValue derived from it and the discount
		// percent, ONCE, at four places — and that taxable value is the number
		// GSTR-1 is filed on, so it is the one figure here that may not be
		// recomputed (see the package comment: re-deriving is a second tax engine).
		//
		// So the gross is derived from the rate, and the discount is whatever is
		// left over. It absorbs both the real discount and the rounding of the
		// books' four places down to NIC's two, and the identity closes EXACTLY —
		// AssAmt is still the books' own taxable value to the paisa.
		//
		// ⚠️ THIS BLOCK ONCE CLAIMED THE DISCOUNT "CANNOT GO NEGATIVE, and that is
		// a property rather than a hope": TaxableValue is qty x rate x (1 - disc),
		// disc is CHECKed into [0, 100] by migration 0025, so taxable <= gross, and
		// rounding is monotonic. ⛔ **EVERY STEP OF THAT IS TRUE AND THE CONCLUSION
		// WAS STILL FALSE**, because the two sides do not descend from the same
		// basis: `lineTaxable` is read back from `voucher_gst_line.taxable_value`,
		// which is NUMERIC(19,4) — Postgres has ALREADY rounded it to four places —
		// while `qty.Mul(*rate)` is the raw product.
		//
		// ⚠️ AND ROUNDING THE GROSS TO FOUR PLACES FIRST DOES NOT FIX IT. That was
		// tried; it moves the failure from the Discount check to the TotAmt check,
		// because the two NIC identities are GENUINELY UNSATISFIABLE TOGETHER at
		// this precision. Measured, on a voucher carrying NO DISCOUNT AT ALL:
		//
		//	qty 0.0011 x rate 10913.6000 = 12.00496 exactly
		//	  the books store  round4(12.00496) = 12.0050  -> AssAmt must be 12.01
		//	  NIC computes     round2(Qty x UnitPrice)     -> TotAmt must be 12.00
		//	  => Discount must be -0.01, and NIC HAS NO SURCHARGE FIELD.
		//
		// There is no rounding that satisfies both: NIC's item block prints two
		// decimals and the books keep four, and `taxable_value` is the figure GSTR-1
		// is filed on, so it is the one number here that may not be recomputed to
		// make the arithmetic close.
		//
		// ⛔ SO THIS IS REFUSED BY NAME RATHER THAN RESOLVED BY GUESSING. The three
		// alternatives were all worse: emitting a negative Discount is a document
		// the IRP rejects; silently restating AssAmt as gross - 0 files a different
		// taxable value from the books; and clamping the discount at zero produces a
		// line whose own three numbers do not foot. **A refusal naming the line and
		// both figures is the only one of the four that does not lie**, and §9.6's
		// rule applies — do not claim a validation, or a reconciliation, that was
		// not performed.
		//
		// ⚠️ THE GENERAL RULE, WORTH MORE THAN THIS LINE: when two numbers "must"
		// be ordered, check they were rounded from the SAME BASIS. A proof about
		// exact arithmetic says nothing about two values that took different paths
		// through a NUMERIC column.
		gross2 := roundMoney(qty.Mul(*rate))
		discount2 := gross2.Sub(taxable2)
		if discount2.IsNegative() {
			return nil, lineTotals{}, fmt.Errorf(
				"%w (line %d, %s: %s x %s rounds to a gross of %s at NIC's two decimals, "+
					"but the books' taxable value for the line rounds to %s — the item block "+
					"would have to carry a discount of %s, and NIC has no surcharge field)",
				ErrPrecisionNotExpressible, lineNo, *name,
				qty.String(), rate.StringFixed(4), gross2.StringFixed(2),
				taxable2.StringFixed(2), discount2.StringFixed(2))
		}

		// ⛔ ACCUMULATE THE ROUNDED VALUES, NOT THE FOUR-PLACE ONES. The invoice
		// total must equal what the document PRINTS, and what it prints is the
		// rounded figure on the line beside it.
		sum.taxable = sum.taxable.Add(taxable2)
		sum.cgst = sum.cgst.Add(cgst2)
		sum.sgst = sum.sgst.Add(sgst2)
		sum.igst = sum.igst.Add(igst2)

		out = append(out, rules.SnapshotLine{
			Desc: *name,
			HSN:  hsn,
			Qty:  qty.String(),
			// UPPER-CASED, and that is the only transformation applied. NIC's UQCs
			// are an upper-case vocabulary (NOS, KGS, MTR) and `unit.symbol` is the
			// company's own free text, so "Nos" is the same unit written the way an
			// operator types it. ⚠️ NO MAPPING IS ATTEMPTED: this build does not
			// translate "kg" to KGS or "pieces" to PCS, because a wrong mapping is a
			// wrong claim about the consignment and the IRP's own refusal names the
			// field precisely. The uppercase is a spelling; a mapping would be a
			// guess.
			Unit: strings.ToUpper(strings.TrimSpace(*unit)),
			// ⛔ THE GROSS RATE, AND NOT ROUNDED TO THE PAISA. It is the multiplicand
			// in NIC's TotAmt = Qty x UnitPrice, so rounding it here would make that
			// product disagree with the gross this line derived from the un-rounded
			// rate — and the residue would land in Discount, sometimes negative. See
			// rules.MoneyAtLeast2.
			UnitPrice:  rules.MoneyAtLeast2(rate.String()),
			Discount:   discount2.StringFixed(2),
			Taxable:    taxable2.StringFixed(2),
			GstRatePct: ratePct.String(),
			CGST:       cgst2.StringFixed(2),
			SGST:       sgst2.StringFixed(2),
			IGST:       igst2.StringFixed(2),
			LineTotal:  taxable2.Add(cgst2).Add(sgst2).Add(igst2).StringFixed(2),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, lineTotals{}, fmt.Errorf("einvoice: load invoice lines: %w", err)
	}

	// ⛔ COUNT BOTH SIDES INDEPENDENTLY. The LEFT JOIN above cannot detect a
	// STOCK movement with no GST line: that row simply never appears, so an
	// invoice charging for three products and shipping four would build a
	// three-item e-invoice and validate cleanly.
	var stockCount, gstCount int
	if err := tx.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM stock_movement   WHERE voucher_version_id = $1),
		       (SELECT count(*) FROM voucher_gst_line WHERE voucher_version_id = $1)`,
		versionID).Scan(&stockCount, &gstCount); err != nil {
		return nil, lineTotals{}, fmt.Errorf("einvoice: count invoice lines: %w", err)
	}
	// ⛔ ORDER MATTERS. A voucher with no tax lines at all is not a malformed
	// invoice, it is not an invoice — say that, rather than "your lines do not
	// correspond", which describes a well-formed receipt as broken.
	if gstCount == 0 {
		return nil, lineTotals{}, ErrNoTaxLines
	}
	if stockCount != gstCount || len(out) != gstCount {
		return nil, lineTotals{}, ErrLineMismatch
	}
	return out, sum, nil
}

// roundMoney quantizes a four-place book figure to the two places NIC requires.
//
// ⛔ decimal.Round, NEVER a float. Money is decimal.Decimal server-side and
// fixed-string on the wire throughout this codebase; a float64 hop here would
// round 0.145 the wrong way about half the time and the error would land inside
// a document carrying a government reference number.
func roundMoney(d decimal.Decimal) decimal.Decimal { return d.Round(2) }
