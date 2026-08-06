// Package ewaybill is cored's WRITE AND READ SIDE for T5 e-way bills: it
// assembles a consignment from the books, runs the pure Rule 138 core over it,
// and stores what came back (migration 0047).
//
// ===========================================================================
// ⛔⛔ WHY THIS IS IN cored AT ALL, WHEN internal/statutory IS A SEPARATE
// DEPLOYABLE
// ===========================================================================
// The full argument is at the top of internal/statutory/rules/ewaybill.go. In
// one paragraph: statutoryd exists because the IRN workflow wraps an
// EXACTLY-ONCE external IRP call in durable, resumable state, and a crash
// between "generate" and "freeze" would mint a second IRN. The e-way bill core
// has none of that — Assess, BuildEWayBill, ValidateEWayBill and
// GenerateEWayBill are pure functions with no context, no error, no clock, no
// socket and no store. Putting them behind a second process would have bought
// nothing and would have moved an operator-facing WRITE outside cored's PDP,
// RLS, step-up and company scoping, none of which statutoryd has.
//
// So this package holds the orchestration and internal/statutory/rules holds
// the rules. This package may reach the database; rules may not, and archtest
// pins it to the empty import set so it never can.
//
// ===========================================================================
// ⛔ WHAT THIS PACKAGE MAY NOT DO: DECIDE A STATUTORY QUESTION
// ===========================================================================
// Every threshold, distance relaxation and validity rate lives in
// rules/rule138.go with its provenance attached, and NOT ONE OF THEM IS
// RESTATED HERE — not in a constant, not in a comparison, not in a message.
// This package assembles facts and stores conclusions. If you find yourself
// writing `50000` or `200` in this file, the figure has acquired a second home
// and the two will disagree the first time one is corrected.
package ewaybill

import (
	"context"
	"encoding/json"
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
// when no e-way bill has been raised for it. A sentinel so the HTTP layer
// answers 404 rather than 500.
var ErrNotFound = errors.New("ewaybill: no such voucher or e-way bill in this company")

// ErrNoGoods refuses a voucher that moves no stock.
//
// ⛔ THIS IS A DOMAIN RULE, NOT A LIMITATION. An e-way bill covers the MOVEMENT
// OF GOODS. A service invoice moves nothing, has no itemList, and needs no bill
// — NIC's schema requires at least one item for the same reason. Generating a
// bill with an empty or invented itemList would produce a document describing a
// consignment that does not exist.
var ErrNoGoods = errors.New(
	"ewaybill: this voucher moves no goods, so no e-way bill can be raised for it — " +
		"an e-way bill covers a movement of goods, and a voucher with no stock lines is not one")

// ErrLineMismatch refuses a voucher whose GST lines and stock lines do not
// correspond one-for-one.
//
// ⛔ IT REFUSES RATHER THAN PAIRING BY POSITION AND HOPING. On an item invoice
// the server GENERATES the GST lines from the stock lines
// (ledger/item_invoice.go: `gstLines := make([]compliance.Line,
// len(in.Inventory))`, filled at the same index), so line N of one IS line N of
// the other and the correspondence is structural.
//
// This package DEPENDS on that and therefore checks it. If the two ever stop
// corresponding — a new voucher path, a migration, a hand-posted voucher
// carrying both — pairing them by position would put one product's HSN and tax
// rate against another product's name and quantity. That is a statutory
// document that is internally consistent, passes every schema check, and is
// wrong about what is in the vehicle. A refusal an operator can read beats a
// document nobody can detect is false.
var ErrLineMismatch = errors.New(
	"ewaybill: this voucher's tax lines and stock lines do not correspond one-for-one, " +
		"so the goods cannot be matched to their tax rates")

// ErrNoTaxLines refuses a voucher that MOVES GOODS BUT CARRIES NO TAX — a
// delivery note, a receipt note or a stock journal.
//
// ⛔ IT IS A SEPARATE SENTINEL FROM ErrLineMismatch, AND THAT IS THE WHOLE
// POINT. Both cases arrive at the same place (a stock-line count that does not
// equal the GST-line count), and the first draft of this package reported both
// as "your tax lines and stock lines do not correspond" — which is TRUE and
// USELESS for a delivery note, because it describes the voucher as malformed
// when it is perfectly well formed and simply carries no tax.
//
// ⚠️ AND THE GAP IS REAL RATHER THAN AN OVERSIGHT. Moving goods on a delivery
// challan without an invoice is an ordinary thing a business does and NIC
// supports it (docType CHL). It is not built here because the CONSIGNMENT VALUE
// would have to be derived from the movement rather than read from the invoice,
// and Assess compares that value against the threshold to decide whether a bill
// is required at all. Deriving it from stock_movement.amount is plausible and is
// NOT obviously right — a delivery note's rate may be a transfer price, or zero.
// Getting it wrong in the permissive direction means a consignment that needed a
// bill did not get one, which is the exact asymmetry rule138.go's threshold
// comment refuses to guess at.
//
// So this refuses, names the gap, and says what to do instead. A wrong figure on
// a statutory document is worse than a refusal an operator can read.
var ErrNoTaxLines = errors.New(
	"ewaybill: this voucher moves goods but carries no GST lines — it is a delivery note, a " +
		"receipt note or a stock journal. Raising an e-way bill against one is a real workflow " +
		"and is NOT built in this version: the consignment value would have to be derived from " +
		"the movement rather than read from the invoice, and this build will not guess at a " +
		"figure the threshold test depends on. Raise the bill against the TAX INVOICE for the " +
		"same goods")

// ErrNotAssessable is returned when the Rule 138 assessment could not compute a
// validity period — which happens exactly when no distance was stated.
//
// ⛔ IT CARRIES THE BLOCKERS RATHER THAN A BARE MESSAGE, because Assess's whole
// design is that it returns its reasoning and the reasoning must reach the
// operator. See rules/ewaybill.go's Assessment.
type ErrNotAssessable struct {
	Blockers []string
	Basis    []string
}

func (e *ErrNotAssessable) Error() string {
	return "ewaybill: the e-way bill cannot be raised as stated: " + strings.Join(e.Blockers, " ")
}

// ErrSchemaViolation is returned when the built payload departs from the
// RECORDED NIC schema (rules.ValidateEWayBill).
//
// ⚠️ A CLEAN VALIDATION IS NOT A CLAIM THAT NIC WOULD ACCEPT THE PAYLOAD, and
// this error's existence must not be read as one. It says the payload conforms
// to what this repository has RECORDED about NIC — see rules/ewaybill_schema.go.
type ErrSchemaViolation struct {
	Violations []string
}

func (e *ErrSchemaViolation) Error() string {
	return "ewaybill: the payload does not conform to the recorded NIC schema: " +
		strings.Join(e.Violations, "; ")
}

// TxRunner is the minimal database capability this package needs — the same
// seam internal/orders takes, and for the same reason: a package that holds a
// pool can start its own transaction outside the caller's, and the RLS GUC is
// set per transaction.
type TxRunner interface {
	RunInTx(ctx context.Context, fn db.TxFunc) error
}

// Service is the e-way bill read/write side over a transaction runner.
type Service struct{ runner TxRunner }

// NewService constructs an e-way bill Service.
func NewService(runner TxRunner) *Service { return &Service{runner: runner} }

// Input is one raise request: the voucher, plus everything about the MOVEMENT
// that the books do not know.
//
// ⛔ EVERY FIELD BELOW THE VOUCHER IS A FACT ABOUT THE WORLD THAT NO LEDGER
// HOLDS. A voucher records that goods were sold; it does not record which lorry
// they left in, how far it is going, or whether the load is over-dimensional.
// That is why they are supplied and not derived — and why OverDimensionalCargo
// in particular has no defaulting anywhere in this file.
type Input struct {
	VoucherID uuid.UUID

	TransporterID   string
	TransporterName string

	// VehicleNumber is PART B. Empty is legal — Assess decides whether this
	// movement requires it, and says why either way.
	VehicleNumber string
	Mode          rules.TransportMode

	// DistanceKm drives the validity period. Zero means NOT STATED and produces
	// ErrNotAssessable; it is never treated as a zero-distance movement.
	DistanceKm int

	// OverDimensionalCargo selects the ODC validity rate.
	//
	// ⛔ ASSERTED BY THE OPERATOR, NEVER INFERRED, AND NEVER DEFAULTED FROM
	// ANYTHING IN THE BOOKS. Guessing "ordinary" grants ten times the validity
	// the rule allows, and an e-way bill the system says is live when it expired
	// is a detained vehicle.
	OverDimensionalCargo bool

	// FromPincode/FromStateCode and ToPincode/ToStateCode override the company
	// and party addresses. Empty means "use the master".
	//
	// ⚠️ THE OVERRIDE EXISTS BECAUSE THE DEFAULT IS OFTEN WRONG, and that is
	// stated rather than hidden: NIC keys the route on where the goods actually
	// start and end, and a consignment routinely ships from a warehouse that is
	// neither party's registered address. Defaulting to the masters is a
	// convenience for the common case, not a claim about the route.
	FromPincode   string
	FromStateCode string
	ToPincode     string
	ToStateCode   string

	CreatedBy string

	// GeneratedAt is when the bill is raised. SUPPLIED, not read from the clock
	// inside this package, so the validity is reproducible and a test can pin it
	// — the same discipline rules.ConsignmentSnapshot.GeneratedAt records.
	GeneratedAt time.Time
}

// View is a raised e-way bill as a caller sees it.
type View struct {
	VoucherID   uuid.UUID
	VoucherNo   string
	VoucherDate string

	// ⛔ LocalReference IS NOT AN EBN. Never label it "e-Way Bill Number".
	LocalReference string
	Provider       string

	Payload     map[string]any
	Disclosures []string

	ValidUntil   string
	ValidityDays int

	AssessedRequired bool
	PartBRequired    bool

	// Blockers are the statutory problems that do NOT prevent the bill being
	// raised but DO prevent the goods moving on it — in practice, a missing
	// vehicle number on a movement that requires Part B.
	//
	// ⚠️ A NON-EMPTY Blockers ON A STORED BILL IS THE PART-A SLIP CASE and is
	// legitimate: an operator raises Part A while the vehicle is still being
	// arranged. The blocker rides in Disclosures too, so it reaches paper.
	Blockers []string

	TransporterID        string
	TransporterName      string
	VehicleNumber        string
	TransportMode        string
	DistanceKm           int
	OverDimensionalCargo bool
	FromPincode          string
	ToPincode            string
	FromStateCode        string
	ToStateCode          string

	GenerationCount int
	GeneratedAt     time.Time

	// Stale reports that the invoice has been ALTERED since this bill was
	// raised, i.e. the stored payload no longer describes the document it names.
	//
	// ⛔ DERIVED ON EVERY READ, NEVER STORED. It is
	// (generated_from_version_id <> voucher_head.current_version_id). A stored
	// copy would go wrong the first time somebody altered a voucher without
	// visiting this table, which is every time — the same argument HANDOFF §8.2
	// makes for refusing a fulfilled_qty column.
	Stale bool
}

// Raise assembles the consignment, applies Rule 138, and stores the result.
//
// It UPSERTS: one live bill per voucher (migration 0047's
// e_way_bill_one_per_voucher), and a second raise for the same voucher replaces
// the payload and increments generation_count. See 0047's header for why
// regeneration is counted rather than either refused or silent.
func (s *Service) Raise(ctx context.Context, in Input) (View, error) {
	if in.VoucherID == uuid.Nil {
		// ⛔ The all-zeros UUID PARSES (HANDOFF §9.5), so an explicit
		// 00000000-... reaches here as a valid uuid. Refuse it rather than
		// letting it become a lookup that finds nothing and reads as "not found".
		return View{}, ErrNotFound
	}
	if strings.TrimSpace(in.CreatedBy) == "" {
		return View{}, errors.New("ewaybill: created_by is required")
	}

	var out View
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		snap, meta, err := loadConsignment(ctx, tx, in)
		if err != nil {
			return err
		}

		// --- THE PURE CORE. Three separate acts, deliberately: Assess decides the
		// statutory questions, BuildEWayBill shapes the payload, ValidateEWayBill
		// checks it against the recorded schema. Collapsing them would mean
		// stopping at the first problem, where an operator fixing a consignment
		// wants the whole list.
		assessment := rules.Assess(snap)

		// ⛔ REFUSE WHEN THE VALIDITY COULD NOT BE COMPUTED, AND ONLY THEN.
		//
		// ValidityDays == 0 happens exactly when no distance was stated. Assess
		// refuses to invent one, and this refuses to store one: migration 0047's
		// valid_until is NOT NULL precisely so "we do not know when this expires"
		// is not a storable state.
		//
		// ⚠️ NOTE WHAT IS *NOT* REFUSED HERE, because the naive reading of
		// Assessment.Blockers is to refuse on any of them. A missing vehicle
		// number on a Part-B movement is also a blocker, and it MUST NOT block:
		// raising Part A while the lorry is still being arranged is a real and
		// ordinary thing an operator does. That blocker travels into the stored
		// disclosures instead, where it reaches the printed sheet as "the goods
		// may not move on this bill until one is added".
		if assessment.ValidityDays == 0 {
			return &ErrNotAssessable{Blockers: assessment.Blockers, Basis: assessment.Basis}
		}

		payload := rules.BuildEWayBill(snap)
		if violations := rules.ValidateEWayBill(payload); len(violations) > 0 {
			return &ErrSchemaViolation{Violations: violations}
		}

		result := rules.FakeEWayBillProvider{}.GenerateEWayBill(payload, assessment)

		payloadJSON, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("ewaybill: marshal payload: %w", err)
		}
		disclosuresJSON, err := json.Marshal(result.Disclosures)
		if err != nil {
			return fmt.Errorf("ewaybill: marshal disclosures: %w", err)
		}

		id, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("ewaybill: new id: %w", err)
		}

		var genCount int
		// ON CONFLICT on (company_id, voucher_id): the second raise for a voucher
		// REPLACES the payload and counts the replacement. `id` is deliberately
		// NOT updated — the row keeps its identity across regenerations, so
		// anything that ever referenced it still resolves.
		err = tx.QueryRow(ctx, `
			INSERT INTO e_way_bill
			  (id, company_id, voucher_id, generated_from_version_id, local_reference,
			   provider, payload, disclosures, assessed_required, part_b_required,
			   valid_until, validity_days, from_pincode, to_pincode,
			   from_state_code, to_state_code, transporter_id, transporter_name,
			   vehicle_number, transport_mode, distance_km, over_dimensional_cargo,
			   generated_at, created_by)
			VALUES
			  ($1, app_current_company_id(), $2, $3, $4,
			   $5, $6, $7, $8, $9,
			   $10, $11, $12, $13,
			   $14, $15, $16, $17,
			   $18, $19, $20, $21,
			   $22, $23)
			ON CONFLICT (company_id, voucher_id) DO UPDATE SET
			  generated_from_version_id = EXCLUDED.generated_from_version_id,
			  local_reference           = EXCLUDED.local_reference,
			  provider                  = EXCLUDED.provider,
			  payload                   = EXCLUDED.payload,
			  disclosures               = EXCLUDED.disclosures,
			  assessed_required         = EXCLUDED.assessed_required,
			  part_b_required           = EXCLUDED.part_b_required,
			  valid_until               = EXCLUDED.valid_until,
			  validity_days             = EXCLUDED.validity_days,
			  from_pincode              = EXCLUDED.from_pincode,
			  to_pincode                = EXCLUDED.to_pincode,
			  from_state_code           = EXCLUDED.from_state_code,
			  to_state_code             = EXCLUDED.to_state_code,
			  transporter_id            = EXCLUDED.transporter_id,
			  transporter_name          = EXCLUDED.transporter_name,
			  vehicle_number            = EXCLUDED.vehicle_number,
			  transport_mode            = EXCLUDED.transport_mode,
			  distance_km               = EXCLUDED.distance_km,
			  over_dimensional_cargo    = EXCLUDED.over_dimensional_cargo,
			  generated_at              = EXCLUDED.generated_at,
			  created_by                = EXCLUDED.created_by,
			  updated_at                = now(),
			  generation_count          = e_way_bill.generation_count + 1
			RETURNING generation_count`,
			id, in.VoucherID, meta.versionID, result.LocalReference,
			result.Provider, payloadJSON, disclosuresJSON, assessment.Required, assessment.PartBRequired,
			assessment.ValidUntil, assessment.ValidityDays, snap.FromPincode, snap.ToPincode,
			snap.FromStateCode, snap.ToStateCode, snap.TransporterID, snap.TransporterName,
			snap.VehicleNumber, string(snap.Mode), snap.DistanceKm, snap.OverDimensionalCargo,
			in.GeneratedAt, in.CreatedBy,
		).Scan(&genCount)
		if err != nil {
			return fmt.Errorf("ewaybill: store e-way bill: %w", err)
		}

		var payloadMap map[string]any
		if err := json.Unmarshal(payloadJSON, &payloadMap); err != nil {
			return fmt.Errorf("ewaybill: re-read payload: %w", err)
		}

		out = View{
			VoucherID:      in.VoucherID,
			VoucherNo:      snap.Invoice.VoucherNo,
			VoucherDate:    snap.Invoice.VoucherDate,
			LocalReference: result.LocalReference,
			Provider:       result.Provider,
			Payload:        payloadMap,
			Disclosures:    result.Disclosures,
			ValidUntil:     assessment.ValidUntil.Format("2006-01-02"),
			ValidityDays:   assessment.ValidityDays,

			AssessedRequired: assessment.Required,
			PartBRequired:    assessment.PartBRequired,
			Blockers:         assessment.Blockers,

			TransporterID:        snap.TransporterID,
			TransporterName:      snap.TransporterName,
			VehicleNumber:        snap.VehicleNumber,
			TransportMode:        string(snap.Mode),
			DistanceKm:           snap.DistanceKm,
			OverDimensionalCargo: snap.OverDimensionalCargo,
			FromPincode:          snap.FromPincode,
			ToPincode:            snap.ToPincode,
			FromStateCode:        snap.FromStateCode,
			ToStateCode:          snap.ToStateCode,

			GenerationCount: genCount,
			GeneratedAt:     in.GeneratedAt,
			// A bill just raised from the CURRENT version is never stale.
			Stale: false,
		}
		return nil
	})
	if err != nil {
		return View{}, err
	}
	return out, nil
}

// Get returns the stored e-way bill for a voucher, or ErrNotFound.
//
// Stale is computed HERE, on every read, by comparing the version the payload
// was built from against the voucher's current version. See View.Stale.
func (s *Service) Get(ctx context.Context, voucherID uuid.UUID) (View, error) {
	if voucherID == uuid.Nil {
		return View{}, ErrNotFound
	}
	var out View
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var (
			payloadJSON     []byte
			disclosuresJSON []byte
			validUntil      time.Time
		)
		err := tx.QueryRow(ctx, `
			SELECT e.local_reference, e.provider, e.payload, e.disclosures,
			       e.assessed_required, e.part_b_required, e.valid_until, e.validity_days,
			       e.from_pincode, e.to_pincode, e.from_state_code, e.to_state_code,
			       e.transporter_id, e.transporter_name, e.vehicle_number, e.transport_mode,
			       e.distance_km, e.over_dimensional_cargo, e.generation_count, e.generated_at,
			       COALESCE(vv.voucher_no, ''),
			       COALESCE(to_char(vv.voucher_date, 'YYYY-MM-DD'), ''),
			       (e.generated_from_version_id <> vh.current_version_id) AS stale
			  FROM e_way_bill e
			  JOIN voucher_head vh
			    ON vh.company_id = e.company_id AND vh.voucher_id = e.voucher_id
			  JOIN voucher_version vv ON vv.id = vh.current_version_id
			 WHERE e.voucher_id = $1`, voucherID).Scan(
			&out.LocalReference, &out.Provider, &payloadJSON, &disclosuresJSON,
			&out.AssessedRequired, &out.PartBRequired, &validUntil, &out.ValidityDays,
			&out.FromPincode, &out.ToPincode, &out.FromStateCode, &out.ToStateCode,
			&out.TransporterID, &out.TransporterName, &out.VehicleNumber, &out.TransportMode,
			&out.DistanceKm, &out.OverDimensionalCargo, &out.GenerationCount, &out.GeneratedAt,
			&out.VoucherNo, &out.VoucherDate, &out.Stale,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("ewaybill: load e-way bill: %w", err)
		}
		out.VoucherID = voucherID
		out.ValidUntil = validUntil.Format("2006-01-02")
		if err := json.Unmarshal(payloadJSON, &out.Payload); err != nil {
			return fmt.Errorf("ewaybill: decode payload: %w", err)
		}
		if err := json.Unmarshal(disclosuresJSON, &out.Disclosures); err != nil {
			return fmt.Errorf("ewaybill: decode disclosures: %w", err)
		}
		// ⛔ Blockers are NOT stored and NOT reconstructed here. They are a
		// property of the ASSESSMENT, and the assessment's conclusions that
		// matter after the fact are already in the disclosures, which is where
		// they must be read from. Re-deriving them on read would run TODAY's
		// rules over YESTERDAY's document — the same reason 0047 stores the
		// disclosure block rather than rebuilding it.
		return nil
	})
	if err != nil {
		return View{}, err
	}
	return out, nil
}

// consignmentMeta is what loadConsignment learned that is not part of the
// snapshot itself.
type consignmentMeta struct {
	versionID uuid.UUID
}

// loadConsignment assembles a rules.ConsignmentSnapshot from the books.
//
// ⛔ THE itemList COMES FROM voucher_gst_line JOINED TO stock_movement ON
// line_no, AND THE JOIN IS CHECKED RATHER THAN ASSUMED — see ErrLineMismatch.
// The tax facts (HSN, taxable value, rate, the CGST/SGST/IGST split) are the
// voucher's OWN per-line GST record, not a re-derivation: re-deriving them here
// would be a second tax engine, and the first divergence would be a statutory
// document disagreeing with the return filed from the same invoice.
func loadConsignment(ctx context.Context, tx pgx.Tx, in Input) (rules.ConsignmentSnapshot, consignmentMeta, error) {
	var (
		meta       consignmentMeta
		voucherNo  string
		vDate      string
		taxable    decimal.Decimal
		cgst       decimal.Decimal
		sgst       decimal.Decimal
		igst       decimal.Decimal
		partyID    *uuid.UUID
		sellerGst  string
		sellerName string
		sellerSt   string
		sellerPin  string
	)

	err := tx.QueryRow(ctx, `
		SELECT vh.current_version_id,
		       COALESCE(vv.voucher_no, ''),
		       COALESCE(to_char(vv.voucher_date, 'YYYY-MM-DD'), ''),
		       COALESCE(vv.taxable_value, 0), COALESCE(vv.cgst, 0),
		       COALESCE(vv.sgst, 0), COALESCE(vv.igst, 0),
		       vv.party_ledger_id,
		       COALESCE(c.gstin, ''), COALESCE(NULLIF(c.mailing_name, ''), c.name),
		       COALESCE(c.state_code, ''), COALESCE(c.pincode, '')
		  FROM voucher_head vh
		  JOIN voucher_version vv ON vv.id = vh.current_version_id
		  JOIN company c ON c.id = vh.company_id
		 WHERE vh.voucher_id = $1 AND NOT vh.is_deleted`, in.VoucherID).Scan(
		&meta.versionID, &voucherNo, &vDate, &taxable, &cgst, &sgst, &igst,
		&partyID, &sellerGst, &sellerName, &sellerSt, &sellerPin)
	if errors.Is(err, pgx.ErrNoRows) {
		return rules.ConsignmentSnapshot{}, meta, ErrNotFound
	}
	if err != nil {
		return rules.ConsignmentSnapshot{}, meta, fmt.Errorf("ewaybill: load voucher: %w", err)
	}

	var buyerGst, buyerName, buyerSt, buyerPin string
	if partyID != nil {
		err = tx.QueryRow(ctx, `
			SELECT COALESCE(gstin, ''), name, COALESCE(state_code, ''), COALESCE(pincode, '')
			  FROM ledger WHERE id = $1`, *partyID).Scan(&buyerGst, &buyerName, &buyerSt, &buyerPin)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return rules.ConsignmentSnapshot{}, meta, fmt.Errorf("ewaybill: load party: %w", err)
		}
	}

	lines, err := loadLines(ctx, tx, meta.versionID)
	if err != nil {
		return rules.ConsignmentSnapshot{}, meta, err
	}

	// The invoice total is DERIVED from the stored components rather than read
	// from a column, because voucher_version has no total column — and it is the
	// figure Assess compares against the threshold, so it must be the same
	// arithmetic the invoice itself foots to.
	total := taxable.Add(cgst).Add(sgst).Add(igst)

	snap := rules.ConsignmentSnapshot{
		Invoice: rules.VoucherSnapshot{
			VoucherNo:       voucherNo,
			VoucherDate:     vDate,
			SellerGstin:     sellerGst,
			SellerLegalName: sellerName,
			SellerStateCode: sellerSt,
			BuyerGstin:      buyerGst,
			BuyerLegalName:  buyerName,
			BuyerStateCode:  buyerSt,
			Lines:           lines,
			Taxable:         taxable.StringFixed(4),
			CGST:            cgst.StringFixed(4),
			SGST:            sgst.StringFixed(4),
			IGST:            igst.StringFixed(4),
			InvoiceTotal:    total.StringFixed(4),
		},
		FromPincode:          firstNonEmpty(in.FromPincode, sellerPin),
		ToPincode:            firstNonEmpty(in.ToPincode, buyerPin),
		FromStateCode:        firstNonEmpty(in.FromStateCode, sellerSt),
		ToStateCode:          firstNonEmpty(in.ToStateCode, buyerSt),
		TransporterID:        in.TransporterID,
		TransporterName:      in.TransporterName,
		VehicleNumber:        in.VehicleNumber,
		Mode:                 in.Mode,
		DistanceKm:           in.DistanceKm,
		OverDimensionalCargo: in.OverDimensionalCargo,
		GeneratedAt:          in.GeneratedAt,
	}
	return snap, meta, nil
}

// loadLines reads the consignment's goods, pairing each stock movement with the
// GST line of the same line_no.
func loadLines(ctx context.Context, tx pgx.Tx, versionID uuid.UUID) ([]rules.SnapshotLine, error) {
	rows, err := tx.Query(ctx, `
		SELECT m.line_no, si.name, COALESCE(u.symbol, ''), m.qty,
		       COALESCE(g.hsn, ''), g.taxable_value, g.gst_rate_pct, g.cgst, g.sgst, g.igst,
		       m.rate
		  FROM stock_movement m
		  JOIN stock_item si ON si.id = m.stock_item_id
		  LEFT JOIN unit u ON u.id = si.unit_id
		  JOIN voucher_gst_line g
		    ON g.voucher_version_id = m.voucher_version_id AND g.line_no = m.line_no
		 WHERE m.voucher_version_id = $1
		 ORDER BY m.line_no`, versionID)
	if err != nil {
		return nil, fmt.Errorf("ewaybill: load consignment lines: %w", err)
	}
	defer rows.Close()

	var out []rules.SnapshotLine
	for rows.Next() {
		var (
			lineNo                          int
			name, unit, hsn                 string
			qty, lineTaxable, rate, ratePct decimal.Decimal
			lineCGST, lineSGST, lineIGST    decimal.Decimal
		)
		if err := rows.Scan(&lineNo, &name, &unit, &qty, &hsn, &lineTaxable, &ratePct,
			&lineCGST, &lineSGST, &lineIGST, &rate); err != nil {
			return nil, fmt.Errorf("ewaybill: scan consignment line: %w", err)
		}
		out = append(out, rules.SnapshotLine{
			Desc:       name,
			HSN:        hsn,
			Qty:        qty.String(),
			Unit:       unit,
			UnitPrice:  rate.StringFixed(4),
			Taxable:    lineTaxable.StringFixed(4),
			GstRatePct: ratePct.String(),
			CGST:       lineCGST.StringFixed(4),
			SGST:       lineSGST.StringFixed(4),
			IGST:       lineIGST.StringFixed(4),
			LineTotal:  lineTaxable.Add(lineCGST).Add(lineSGST).Add(lineIGST).StringFixed(4),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ewaybill: load consignment lines: %w", err)
	}

	// ⛔ THE TWO COUNTS MUST AGREE, AND THE INNER JOIN ABOVE IS NOT ENOUGH TO
	// PROVE IT. A movement with no matching GST line is DROPPED by the join
	// rather than refused — which would silently ship an e-way bill describing
	// fewer goods than the vehicle carries. So count both sides independently.
	var stockCount, gstCount int
	if err := tx.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM stock_movement  WHERE voucher_version_id = $1),
		       (SELECT count(*) FROM voucher_gst_line WHERE voucher_version_id = $1)`,
		versionID).Scan(&stockCount, &gstCount); err != nil {
		return nil, fmt.Errorf("ewaybill: count consignment lines: %w", err)
	}
	if stockCount == 0 {
		return nil, ErrNoGoods
	}
	// ⛔ THE ORDER OF THESE TWO MATTERS. A voucher that moves goods and carries
	// NO tax is a delivery note, and calling that "your lines do not correspond"
	// describes a well-formed voucher as malformed. Name the real case first.
	if gstCount == 0 {
		return nil, ErrNoTaxLines
	}
	if stockCount != gstCount || len(out) != stockCount {
		return nil, ErrLineMismatch
	}
	return out, nil
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
