package server

// HTTP surface for the TDS CHALLAN — T6, migration 0069.
//
//	GET  .../tds-challans?tax_year_start=2026        -> Display / tds-challan
//	POST .../tds-challans                            -> Create  / tds-challan
//	POST .../tds-challans/{challanID}/allocations    -> Create  / tds-challan
//
// This is the deposit slip: what was paid to the Government, when, through which
// bank branch, and — the half that makes a return buildable at all — WHICH
// DEDUCTIONS IT PAID. Deductee Detail field 5 of the prescribed return is a
// challan reference, so a deduction with no allocation cannot be filed; before
// this route existed the only way to record one was raw SQL, which is exactly how
// internal/reporting's tests seed it today.
//
// ---------------------------------------------------------------------------
// ONE RESOURCE TOKEN FOR THE READ AND THE WRITE, AND IT IS A BARE TOKEN AND NOT
// A REPORT NAME. That is the decision in this file.
// ---------------------------------------------------------------------------
// A `report:tds-challan` would be GRANTED to the seeded Data Entry level by
// migration 0011's coarse `allow display report`: internal/authz matches a bare
// `report` rule against a request for ANY `report:NAME` at specificity 1, so the
// moment this became a report name it would carry an authority nobody chose for
// it, and taking it back would need a counterweight migration — which is exactly
// why migration 0068 exists for report:tds-register.
//
// A BARE TOKEN NEEDS NO MIGRATION. The seeded Data Entry level has
// default_access 'none' and every rule it carries names a specific token, so a
// token never mentioned is denied; the Owner's 'full' allows both halves. That
// was MEASURED on a freshly-migrated database, and authz/vocabulary.go records
// the identical decision, in the identical words, for `deductor-status`.
//
// STATE THE CONSEQUENCE PLAINLY, because it is a product behaviour and not an
// implementation detail: DATA ENTRY IS REFUSED BOTH ROUTES WITH NO RULE WRITTEN.
// An administrator who wants a clerk recording challans writes an explicit
// `allow create tds-challan` through the admin API. That is a tenant's decision,
// taken deliberately, rather than a default this software chose on their behalf —
// and the separate token is what makes the delegation POSSIBLE without also
// handing over the deductor profile next door, which names an individual's PAN.
//
// ⛔ AND THE TOKEN MUST NOT BE ADDED TO authz.KnownReportNames. server.go loops
// over that list and AUTO-MOUNTS GET /print/reports/<name>; a name with no
// matching reportName() case produces a route routeAction cannot map, which is
// default-DENY for every level including the Owner. That bug already shipped once
// for reconciliation-queue (auth_middleware.go records it).
//
// ---------------------------------------------------------------------------
// WHY EVERY WRITE IS A POST AND THERE IS NO PUT, NO PATCH AND NO DELETE
// ---------------------------------------------------------------------------
// A challan is an EVENT that happened at a bank counter, not a row an operator
// maintains. It has no natural key an operator would restate — the CIN is unique
// and re-posting the same one is ErrDuplicateTDSChallanCIN and never an upsert,
// because the second post of one CIN is either a double entry or a second real
// deposit whose number was mistyped, and silently overwriting either loses money.
//
// ⛔ THE SECOND POST IS AN ADD, NOT AN EDIT (register row D1). A deposit may
// legitimately be recorded naming fewer deductions than it paid for, and until
// this route existed the remainder could NEVER be attached to it — the audit
// drove seven in-browser doors and six HTTP methods at challan 05130 and its
// unallocated balance stayed at exactly 12000.0000, while the return refused the
// unattached deduction as undeposited. So .../allocations is a POST because it
// CREATES allocations that were not there; it cannot move one off another
// challan, change an amount or remove anything. Correcting a challan still means
// correcting it in the database, and masters/tds_challan.go's header says what
// the right shape for that would be.
//
// ---------------------------------------------------------------------------
// MONEY ON THE WIRE
// ---------------------------------------------------------------------------
// Every amount in and out is a FIXED-POINT DECIMAL STRING, parsed with
// decimal.NewFromString and never strconv.ParseFloat, and rendered with
// StringFixed(4) to match the NUMERIC(19,4) columns and every other money field
// this API serves. The challan's own five amounts are WHOLE RUPEES (migration
// 0069's tds_challan_amounts_whole_rupees — the format forbids paise in a challan
// box), so they will read "5000.0000"; that is deliberate uniformity at the seam
// rather than a second money format for one table.
//
// ⚠️ unallocated_tax_rupees IS THE ONE FIELD THAT IS GENUINELY NOT WHOLE RUPEES,
// and it CAN BE NEGATIVE. tds_deduction.tds_amount_rupees is rounded to 4 dp
// (₹33,333 at 10% is ₹3,333.30) while the deposit against it must be a whole
// number, so a difference of a few paise per allocation is the ordinary case and
// not an error. The difference is REPORTED so a human can read it, and this
// route still invents no equality: whether the prescribed format requires a
// challan's tax total to equal the sum of its deductee "tax deposited" figures
// is UNVERIFIED, and requiring it would refuse legitimate rounded deposits.
//
// ⛔ WHAT DID CHANGE, AND WHY IT IS NOT THAT EQUALITY (register row D2). The
// store now refuses an allocation total that exceeds the deposit BY A RUPEE OR
// MORE PER ALLOCATION — the largest difference rounding can produce — because
// nothing compared the two at all and a ₹1 deposit was accepted as having paid
// ₹27,000.0000 of tax across two deductees. Both were then marked "already
// deposited", so the real deposit could never be allocated to them, and
// unallocated_tax_rupees read -26999.0000 under a caption written for the ordinary
// paise remainder. UNDER-allocation is still legal and still just reported; only
// the over-claim is refused. See masters.overAllocationRefusal for the derivation
// of the tolerance, and do not turn it into an equality.

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/masters"
)

// ---------------------------------------------------------------------------
// The wire shapes.
// ---------------------------------------------------------------------------

// tdsChallanAllocationReq is one deduction this deposit paid.
type tdsChallanAllocationReq struct {
	// VoucherID, Section and LegSeq are tds_deduction's NATURAL key — never its
	// surrogate id, which internal/ledger regenerates on every voucher ALTER.
	VoucherID string `json:"voucher_id"`
	Section   string `json:"section"`
	// LegSeq is a POINTER because it is 0-based: leg 0 is a real leg, so an
	// absent field cannot be defaulted to it. A client that forgot the field
	// would otherwise silently allocate to the first leg of the voucher, and the
	// only thing that would catch it is the amount equality happening to
	// disagree — which it will not when the voucher has one leg under that
	// section. Refused by name instead.
	LegSeq *int `json:"leg_seq"`
	// Kind is "TDS" (a deduction, migration 0050) or "TCS" (a collection,
	// migration 0067). It is served on every allocatable row and sent back
	// unchanged, exactly as voucher_id and leg_seq are.
	//
	// ⚠️ EMPTY MEANS TDS, which is the OPPOSITE call from LegSeq directly above,
	// and the difference is whether a wrong default can pass silently. An omitted
	// leg_seq means leg 0, which is a real leg and would be recorded as paid with
	// nothing to catch it. An omitted kind means a lookup in tds_deduction, and
	// the two section vocabularies are disjoint by CHECK — so if the operator
	// meant a collection, NOTHING matches and the request is refused by name. The
	// default also keeps every client written before D4 correct rather than
	// silently wrong.
	Kind string `json:"kind"`
	// AmountRupees is a fixed-point string, and it must EQUAL the tax the
	// deduction shows. The store refuses any inequality and its message says
	// why: reporting treats an inequality as an allocation broken by an ALTER
	// and refuses the whole quarter, so a deliberately partial allocation would
	// surface at filing time as a message about a change nobody made.
	AmountRupees string `json:"amount_rupees"`
}

// tdsChallanReq is the body of POST .../tds-challans.
type tdsChallanReq struct {
	// BSRCode is the seven-digit code of the bank branch that received the
	// deposit, or — for a book-entry deposit — the seven-digit Form 24G receipt
	// number, which the file format carries in the same field. IT IS TEXT AND
	// LEADING ZEROS ARE PART OF IT: 0004329 is a valid code and 4329 is a
	// different branch. A client that ever holds it in a number type has already
	// lost the information.
	BSRCode string `json:"bsr_code"`
	// DepositDate is yyyy-mm-dd: the date the tax was PAID TO THE GOVERNMENT.
	// Not the voucher date and not the deduction date.
	DepositDate string `json:"deposit_date"`
	// ChallanNo is the one-to-five-digit serial the bank printed on the
	// counterfoil. Text, for the same reason as the BSR code: 05122 is not 5122.
	ChallanNo string `json:"challan_no"`
	// ByBookEntry selects the format's mode of payment: book adjustment for a
	// Government transfer voucher, bank challan otherwise.
	ByBookEntry bool `json:"by_book_entry"`
	// The four amounts, as the challan itself splits them. FOUR SEPARATE FACTS
	// and not a total with a breakdown — only the TAX component discharges a
	// deduction liability (migration 0069), which is why an interest-only challan
	// allocates to nothing and a challan with tax must name what it paid.
	//
	// An EMPTY string is zero here, and that is the opposite call from
	// /deductor-status's turnover. There, empty means "the test does not apply"
	// and 0 would be a false claim about the company. Here a blank box on a
	// challan and a zero in it are the same statement: nothing was paid under
	// that head. A challan on which all four are zero is refused by the store —
	// a nil challan is derived by the emitter and never stored.
	TaxRupees      string `json:"tax_rupees"`
	InterestRupees string `json:"interest_rupees"`
	FeeRupees      string `json:"fee_rupees"`
	PenaltyRupees  string `json:"penalty_rupees"`
	// MinorHead is '200', '400' or '100' — served by the GET as minor_heads.
	// MANDATORY, with NO DEFAULT anywhere on this path: defaulting to 200 would
	// assert on the company's behalf that the deposit was voluntary rather than
	// demanded, which is a statement about its assessment history.
	MinorHead string `json:"minor_head"`
	// TaxYearStart is the year the DEPOSIT BELONGS TO (2026 = 2026-27). Recorded,
	// never computed from the deposit date: a challan paid on 7 April 2026 is
	// normally for March of 2025-26 and the date alone cannot tell you which.
	TaxYearStart int                       `json:"tax_year_start"`
	Allocations  []tdsChallanAllocationReq `json:"allocations"`
	RecordedBy   string                    `json:"recorded_by"`
}

// tdsChallanAllocationView is one allocation read back, with the LIVE deduction
// beside it so a screen can show an allocation the books have outgrown.
type tdsChallanAllocationView struct {
	VoucherID string `json:"voucher_id"`
	VoucherNo string `json:"voucher_no"`
	Section   string `json:"section"`
	LegSeq    int    `json:"leg_seq"`
	// Kind is "TDS" or "TCS", recovered from which table carries this natural
	// key. A screen needs it to caption the party correctly: on a TCS row the
	// name is the BUYER the tax was collected FROM, not a deductee it was paid to.
	Kind string `json:"kind"`
	// DeducteeName is empty when DeductionOnRecord is false — the deduction is
	// gone, so there is nobody to name.
	DeducteeName    string `json:"deductee_name"`
	AllocatedRupees string `json:"allocated_rupees"`
	// DeductionOnRecord false means an ALTER removed the leg. The allocation
	// SURVIVES that (migration 0069 carries no foreign key to tds_deduction, by
	// design) and is shown rather than silently dropped: it records a real
	// payment.
	DeductionOnRecord  bool   `json:"deduction_on_record"`
	DeductionTaxRupees string `json:"deduction_tax_rupees"`
	// BrokenByAlter is ADVISORY and is sent so the operator meets it before
	// filing day. reporting.Form140StatementFor applies the same test and
	// REFUSES the quarter (ErrAllocationBrokenByAlter); that refusal remains the
	// authority. A screen must not present this flag as "the return is fine".
	BrokenByAlter bool `json:"broken_by_alter"`
}

// tdsChallanView is one challan read back, with what it paid for.
type tdsChallanView struct {
	ID          string `json:"id"`
	BSRCode     string `json:"bsr_code"`
	DepositDate string `json:"deposit_date"`
	ChallanNo   string `json:"challan_no"`
	ByBookEntry bool   `json:"by_book_entry"`

	TaxRupees      string `json:"tax_rupees"`
	InterestRupees string `json:"interest_rupees"`
	FeeRupees      string `json:"fee_rupees"`
	PenaltyRupees  string `json:"penalty_rupees"`
	// TotalDepositedRupees is DERIVED from the four and never stored — a stored
	// total is a column that disagrees with its parts after the first
	// correction.
	TotalDepositedRupees string `json:"total_deposited_rupees"`

	MinorHead string `json:"minor_head"`
	// MinorHeadDescription travels with the code so a list row can read
	// "400 — a demand raised on assessment" without the client holding a copy of
	// a closed vocabulary the migration owns.
	MinorHeadDescription string `json:"minor_head_description"`
	TaxYearStart         int    `json:"tax_year_start"`
	TaxYearLabel         string `json:"tax_year_label"`

	AllocationCount      int    `json:"allocation_count"`
	AllocatedTotalRupees string `json:"allocated_total_rupees"`
	// UnallocatedTaxRupees CAN BE NEGATIVE and a few paise either way is the
	// ordinary case. See the file header before adding a rule about it.
	UnallocatedTaxRupees string `json:"unallocated_tax_rupees"`

	Allocations []tdsChallanAllocationView `json:"allocations"`

	RecordedBy string `json:"recorded_by"`
	RecordedAt string `json:"recorded_at"`
}

func toTDSChallanView(c masters.TDSChallanRecord) tdsChallanView {
	allocs := make([]tdsChallanAllocationView, 0, len(c.Allocations))
	for _, a := range c.Allocations {
		allocs = append(allocs, tdsChallanAllocationView{
			VoucherID:          a.VoucherID.String(),
			VoucherNo:          a.VoucherNo,
			Section:            a.Section,
			LegSeq:             a.LegSeq,
			Kind:               string(a.Kind),
			DeducteeName:       a.DeducteeName,
			AllocatedRupees:    a.AllocatedRupees.StringFixed(4),
			DeductionOnRecord:  a.DeductionOnRecord,
			DeductionTaxRupees: a.DeductionTaxRupees.StringFixed(4),
			BrokenByAlter:      a.BrokenByAlter,
		})
	}
	return tdsChallanView{
		ID:                   c.ID.String(),
		BSRCode:              c.BSRCode,
		DepositDate:          c.DepositDate,
		ChallanNo:            c.ChallanNo,
		ByBookEntry:          c.ByBookEntry,
		TaxRupees:            c.TaxRupees.StringFixed(4),
		InterestRupees:       c.InterestRupees.StringFixed(4),
		FeeRupees:            c.FeeRupees.StringFixed(4),
		PenaltyRupees:        c.PenaltyRupees.StringFixed(4),
		TotalDepositedRupees: c.TotalDepositedRupees.StringFixed(4),
		MinorHead:            c.MinorHead,
		MinorHeadDescription: c.MinorHeadDescription,
		TaxYearStart:         c.TaxYearStart,
		TaxYearLabel:         taxYearLabel(c.TaxYearStart),
		AllocationCount:      c.AllocationCount,
		AllocatedTotalRupees: c.AllocatedTotalRupees.StringFixed(4),
		UnallocatedTaxRupees: c.UnallocatedTaxRupees.StringFixed(4),
		Allocations:          allocs,
		RecordedBy:           c.RecordedBy,
		RecordedAt:           c.RecordedAt,
	}
}

// minorHeadViews is the Annexure 7 vocabulary as the client receives it. SERVED
// and never hard-coded in TypeScript, for the reason handler_deductor_status.go
// gives: a copy in the client is a copy no test can pin, and it could offer a
// code migration 0069's CHECK refuses on save. The DESCRIPTION travels with the
// code because the operator has to choose one and there is no default — a picker
// of three bare numbers invites whichever is listed first.
func minorHeadViews() []map[string]string {
	heads := masters.TDSChallanMinorHeads()
	out := make([]map[string]string, 0, len(heads))
	for _, h := range heads {
		out = append(out, map[string]string{"code": h.Code, "description": h.Description})
	}
	return out
}

// ---------------------------------------------------------------------------
// GET .../tds-challans?tax_year_start=YYYY
// ---------------------------------------------------------------------------

// handleListTDSChallans serves the challan register for ONE tax year.
//
// THE TAX YEAR IS REQUIRED AND THERE IS NO DEFAULT, which is the register
// route's decision made again for the same reason: a correctly rendered list of
// the wrong year's deposits is worse than a refusal, because it reads exactly
// like the answer the operator asked for. "This year" is also not derivable here
// — a deposit made on 7 April normally belongs to the year that ended a week
// earlier, which is precisely why migration 0069 STORES the year.
func (s *Server) handleListTDSChallans(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("tax_year_start")
	if raw == "" {
		writeError(w, http.StatusBadRequest,
			"the challan register needs tax_year_start (the calendar year the tax year "+
				"begins in, e.g. 2026 for 2026-27). It is not defaulted from today's date: a "+
				"challan paid on 7 April belongs to the year that ended a week earlier, which "+
				"is why the year is recorded on the challan rather than computed from it")
		return
	}
	taxYear, err := strconv.Atoi(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest,
			"tax_year_start must be the calendar year the tax year begins in, e.g. 2026 for 2026-27")
		return
	}

	rows, err := s.masters.ListTDSChallans(r.Context(), taxYear)
	if err != nil {
		writeMastersError(w, err)
		return
	}
	views := make([]tdsChallanView, 0, len(rows))
	for _, row := range rows {
		views = append(views, toTDSChallanView(row))
	}
	// WHAT CAN BE ALLOCATED, served with the register.
	//
	// ⛔ IT IS SERVED BECAUSE THE FORM IS UNUSABLE WITHOUT IT. An allocation is
	// keyed on (voucher_id, section, leg_seq) — a UUID and a 0-based ordinal — and
	// NO SCREEN IN THIS PRODUCT DISPLAYS EITHER: the TDS/TCS Register renders
	// voucher_no and its payload has no leg field at all. A form that asks a human
	// to type them asks for something they cannot obtain, and typing the one thing
	// they can see ("P1") earns "allocation 1: invalid voucher_id".
	//
	// So the identifiers travel to the client and back WITHOUT a human ever seeing
	// them, and the amount travels too: RecordTDSChallan refuses an allocation that
	// is not EQUAL to the deduction's tax, so there is nothing to type there
	// either. The operator picks a deduction by voucher number and party name.
	//
	// A read failure here does NOT fail the request: the register itself is still
	// worth showing, and the screen can say the picker is unavailable. Returning
	// 422 for the whole page would hide the deposits an operator came to look at.
	allocatable, allocErr := s.masters.ListAllocatableDeductions(r.Context(), taxYear)
	body := map[string]any{
		"tax_year_start":         taxYear,
		"tax_year_label":         taxYearLabel(taxYear),
		"challans":               views,
		"minor_heads":            minorHeadViews(),
		"allocatable_deductions": allocatableViews(allocatable),
	}
	if allocErr != nil {
		body["allocatable_error"] = allocErr.Error()
	}
	writeJSON(w, http.StatusOK, body)
}

// allocatableDeductionView is one deduction a new challan could pay for.
type allocatableDeductionView struct {
	// VoucherID and LegSeq are here so the client can send them back, NOT so a
	// human can read them. See the note at the call site.
	VoucherID string `json:"voucher_id"`
	LegSeq    int    `json:"leg_seq"`
	// Kind is "TDS" or "TCS". It travels for the same reason the ids do — the
	// write path needs it to know which table to verify the triple against — and
	// a screen needs it to say whether the row is tax DEDUCTED from a supplier or
	// COLLECTED from a buyer.
	Kind          string `json:"kind"`
	VoucherNo     string `json:"voucher_no"`
	DeducteeName  string `json:"deductee_name"`
	Section       string `json:"section"`
	DeductionDate string `json:"deduction_date"`
	// TaxRupees is exactly what may be allocated, so the client sends it back
	// unchanged rather than offering an amount field.
	TaxRupees string `json:"tax_rupees"`
	// AlreadyPaid is served rather than filtered so an operator who cannot find a
	// deduction learns WHICH reason applies: not deducted in this year, or already
	// deposited.
	AlreadyPaid bool `json:"already_paid"`
}

func allocatableViews(in []masters.AllocatableDeduction) []allocatableDeductionView {
	out := make([]allocatableDeductionView, 0, len(in))
	for _, a := range in {
		out = append(out, allocatableDeductionView{
			VoucherID:     a.VoucherID.String(),
			LegSeq:        a.LegSeq,
			Kind:          string(a.Kind),
			VoucherNo:     a.VoucherNo,
			DeducteeName:  a.DeducteeName,
			Section:       a.Section,
			DeductionDate: a.DeductionDate,
			TaxRupees:     a.TaxRupees.StringFixed(4),
			AlreadyPaid:   a.AlreadyPaid,
		})
	}
	return out
}

// ---------------------------------------------------------------------------
// POST .../tds-challans
// ---------------------------------------------------------------------------

// handleCreateTDSChallan records one deposit and everything it paid for, in one
// transaction (masters.RecordTDSChallan does the work; this decodes and maps).
//
// EVERY REFUSAL THIS ROUTE CAN MAKE IS THE OPERATOR'S INPUT OR THE BOOKS' STATE,
// never the server's, and the five sentinels map to two different HTTP families
// on purpose — see the mapping below.
func (s *Server) handleCreateTDSChallan(w http.ResponseWriter, r *http.Request) {
	var req tdsChallanReq
	if !decodeJSON(w, r, &req) {
		return
	}

	in := masters.TDSChallanInput{
		BSRCode:      req.BSRCode,
		DepositDate:  req.DepositDate,
		ChallanNo:    req.ChallanNo,
		ByBookEntry:  req.ByBookEntry,
		MinorHead:    req.MinorHead,
		TaxYearStart: req.TaxYearStart,
	}

	// ---- Parse the money. decimal.NewFromString, never ParseFloat. ---------
	// optionalDecimal treats "" as zero, which for a challan box is the same
	// statement as 0 (see the request type). EACH FIELD IS NAMED IN ITS OWN
	// refusal: "an amount is invalid" sends the operator to check all four, and
	// three of the four are usually blank.
	for _, f := range []struct {
		name string
		raw  string
		into *decimal.Decimal
	}{
		{"tax_rupees", req.TaxRupees, &in.TaxRupees},
		{"interest_rupees", req.InterestRupees, &in.InterestRupees},
		{"fee_rupees", req.FeeRupees, &in.FeeRupees},
		{"penalty_rupees", req.PenaltyRupees, &in.PenaltyRupees},
	} {
		d, perr := optionalDecimal(f.raw)
		if perr != nil {
			writeError(w, http.StatusBadRequest,
				"invalid "+f.name+": money is a fixed-point decimal STRING such as \"5000\" — "+
					"never a JSON number, which cannot carry paise exactly")
			return
		}
		*f.into = d
	}

	// ---- The allocations. -------------------------------------------------
	allocations, ok := buildChallanAllocations(w, req.Allocations)
	if !ok {
		return
	}
	in.Allocations = allocations

	id, err := s.masters.RecordTDSChallan(r.Context(), in, req.RecordedBy)
	if err != nil {
		writeChallanError(w, err)
		return
	}

	out := map[string]any{
		"id":             id.String(),
		"tax_year_start": req.TaxYearStart,
		"tax_year_label": taxYearLabel(req.TaxYearStart),
	}
	// READ BACK, and the reason is the one field this API cannot compute for the
	// client: unallocated_tax_rupees. The challan's tax is whole rupees and the
	// deductions it paid are 4-dp figures, so a difference of paise is normal and
	// the operator should meet it at the moment they record the deposit rather
	// than on filing day. The read also carries the minor-head description and
	// the advisory broken-by-alter flags.
	//
	// ⚠️ A FAILURE OF THE READ-BACK IS NOT A FAILURE OF THE WRITE. The challan is
	// committed by the time we get here, so answering 4xx/422 would tell the
	// operator their deposit was not recorded when it was — and the next thing
	// they would do is record it again, which the CIN constraint would then
	// refuse in a way that reads like a bug. 201 stands, and the failed read is
	// stated in its own field.
	out["challan"] = nil
	rows, rerr := s.masters.ListTDSChallans(r.Context(), req.TaxYearStart)
	if rerr != nil {
		out["read_back_error"] = rerr.Error()
	}
	for _, row := range rows {
		if row.ID == id {
			out["challan"] = toTDSChallanView(row)
			break
		}
	}
	writeJSON(w, http.StatusCreated, out)
}

// buildChallanAllocations decodes the allocation grid, writing a 400 and
// returning ok=false on the first malformed line.
//
// ⛔ ONE DECODER FOR BOTH WRITE PATHS. The create and the allocate-later route
// send the SAME line shape, and a second copy of these three refusals is a copy
// that drifts — with the drifted one winning for whichever caller reaches it
// first. That is the shape §7.2 of the parity audit records six times over for
// the cutoff's peeks, one layer down.
//
// The kind is NOT validated here: masters.normaliseAllocationKind owns the
// vocabulary and refuses an unknown one by name, and a second copy of a closed
// vocabulary in the HTTP layer is exactly what the minor heads are SERVED to
// avoid.
func buildChallanAllocations(w http.ResponseWriter,
	in []tdsChallanAllocationReq) ([]masters.TDSChallanAllocationInput, bool) {
	out := make([]masters.TDSChallanAllocationInput, 0, len(in))
	for i, a := range in {
		at := "allocation " + strconv.Itoa(i+1)
		voucherID, verr := optionalUUID(a.VoucherID)
		if verr != nil {
			writeError(w, http.StatusBadRequest, at+": invalid voucher_id")
			return nil, false
		}
		// 0 IS A REAL LEG, so an absent leg_seq is refused rather than defaulted.
		// Defaulting would allocate to the first leg of the voucher, and nothing
		// downstream would catch it whenever that voucher carries a single
		// deduction under the section — the amount equality would pass and the
		// wrong leg would be recorded as paid.
		if a.LegSeq == nil {
			writeError(w, http.StatusBadRequest,
				at+": leg_seq is required and is NOT defaulted. The deduction key is "+
					"(voucher, section, leg) and the leg ordinal is 0-based, so leg 0 is a real "+
					"leg — an omitted field would silently mean the first one")
			return nil, false
		}
		amount, aerr := decimal.NewFromString(a.AmountRupees)
		if aerr != nil {
			writeError(w, http.StatusBadRequest,
				at+": amount_rupees must be a fixed-point decimal string, and it must be the "+
					"tax that was deducted on that leg — it is what makes a later voucher ALTER "+
					"detectable, so it is stated rather than derived")
			return nil, false
		}
		out = append(out, masters.TDSChallanAllocationInput{
			VoucherID:    voucherID,
			Section:      a.Section,
			LegSeq:       *a.LegSeq,
			Kind:         masters.AllocationKind(a.Kind),
			AmountRupees: amount,
		})
	}
	return out, true
}

// ---------------------------------------------------------------------------
// POST .../tds-challans/{challanID}/allocations
// ---------------------------------------------------------------------------

// tdsChallanAllocateReq is the body of POST .../tds-challans/{challanID}/allocations.
//
// ⛔ IT CARRIES NO DATE, AND THAT IS DELIBERATE. The deposit is already recorded
// and its deposit_date is already stored; an allocation adopts it. A date on this
// body would be a second, unchecked, statement about when money reached the
// Government — and the back-dated cutoff would then have two dates to reconcile
// for one act. The cutoff instead reads the STORED deposit date of the challan
// named in the path (see storedChallanDepositDate in auth_middleware.go), which
// is the round-3 rule — judge the date the books already hold — applied to the
// one end this act has.
type tdsChallanAllocateReq struct {
	Allocations []tdsChallanAllocationReq `json:"allocations"`
	RecordedBy  string                    `json:"recorded_by"`
}

// handleAllocateToTDSChallan attaches further allocations to a challan that is
// already recorded (register row D1).
//
// It answers 200, not 201: the CHALLAN is what this resource is, and it existed
// before the request. What is created is a link, and the operator's question is
// "what does this deposit now cover" — which the read-back answers.
func (s *Server) handleAllocateToTDSChallan(w http.ResponseWriter, r *http.Request) {
	challanID, ok := parseChallanID(w, r)
	if !ok {
		return
	}
	var req tdsChallanAllocateReq
	if !decodeJSON(w, r, &req) {
		return
	}
	allocations, ok := buildChallanAllocations(w, req.Allocations)
	if !ok {
		return
	}

	taxYear, err := s.masters.AllocateToRecordedChallan(r.Context(), challanID, allocations, req.RecordedBy)
	if err != nil {
		writeChallanError(w, err)
		return
	}

	// READ BACK, for the same reason the create does and one more: the whole
	// point of this route is that a challan's UNALLOCATED balance must FALL, and
	// the audit's own oracle for register row D1 is that number. Sending it back
	// means the operator (and any test) reads the result of the write rather than
	// inferring it from a status code — this repo's signature failure is a 200
	// that changed nothing, and a 403 that wrote anyway.
	//
	// ⚠️ A FAILURE OF THE READ-BACK IS NOT A FAILURE OF THE WRITE. The allocations
	// are committed by the time we get here; answering 4xx would tell the operator
	// their deposit is still unallocated and invite them to send it again, which
	// the one-challan-per-deduction constraint would then refuse in a way that
	// reads like a bug.
	out := map[string]any{
		"id":             challanID.String(),
		"tax_year_start": taxYear,
		"tax_year_label": taxYearLabel(taxYear),
		"challan":        nil,
	}
	rows, rerr := s.masters.ListTDSChallans(r.Context(), taxYear)
	if rerr != nil {
		out["read_back_error"] = rerr.Error()
	}
	for _, row := range rows {
		if row.ID == challanID {
			out["challan"] = toTDSChallanView(row)
			break
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// parseChallanID reads {challanID} out of the path.
//
// A malformed uuid is a 400 NAMING THE SEGMENT, never a 404: "not found" would
// tell the operator the deposit is missing when what is wrong is the link they
// followed. ⚠️ A well-formed id for a challan this company does not have IS a
// 404, raised by the store as masters.ErrNotFound — and another tenant's challan
// is indistinguishable from a missing one, which is the intended answer under
// RLS.
func parseChallanID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	raw := chi.URLParam(r, "challanID")
	id, err := uuid.Parse(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest,
			"the challan id in the path is not a uuid. It is the `id` the register serves for "+
				"each recorded deposit, and no human is expected to type it")
		return uuid.Nil, false
	}
	return id, true
}

// writeChallanError maps the store's five sentinels onto two HTTP families.
//
// 400 — THE REQUEST IS WRONG AND THE OPERATOR CAN FIX IT BY CHANGING IT:
//
//   - ErrInvalidTDSChallan: a shape or self-consistency rule (the BSR code, the
//     minor head, the tax year, a challan with tax and no allocations).
//
//   - ErrNoSuchDeduction: the allocation names a (voucher, section, leg) no
//     deduction has. NOT a 404: the challan is what is being created and it does
//     not exist yet, so a 404 would be read as "this route is wrong".
//
//   - ErrTDSAllocationMismatch: the amount is not the tax that was deducted, or
//     the deduction is a sub-threshold zero that belongs to the return's derived
//     nil challan.
//
//   - ErrChallanOverAllocated: the allocations claim more tax than the deposit
//     contains. 400 and not 409, because the body IS wrong — either the deposit
//     box or one of the lines — and changing it is exactly what fixes it.
//
//   - ErrMixedChallanKinds: one challan asked to pay both a deduction and a
//     collection. 400 for the same reason: the remedy is to record the
//     collection's deposit on its own challan, which is a different request.
//
// 409 — THE REQUEST IS WELL FORMED AND THE BOOKS ALREADY SAY OTHERWISE. Nothing
// in the body is wrong; changing a field would not help, and RETRYING WOULD BE
// THE MISTAKE:
//   - ErrDuplicateTDSChallanCIN: this deposit slip is already recorded. Either a
//     double entry, or a second real deposit whose serial was mistyped. Never an
//     upsert — overwriting either loses money.
//   - ErrDeductionAlreadyPaid: another challan already paid that deduction, and
//     the usual reason an operator is here is that they are about to pay it
//     twice. The store's sentence names the challan that already did.
//
// Everything else is 422, matching every neighbouring masters route.
func writeChallanError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, masters.ErrInvalidTDSChallan),
		errors.Is(err, masters.ErrNoSuchDeduction),
		errors.Is(err, masters.ErrChallanOverAllocated),
		errors.Is(err, masters.ErrMixedChallanKinds),
		errors.Is(err, masters.ErrTDSAllocationMismatch):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, masters.ErrDuplicateTDSChallanCIN),
		errors.Is(err, masters.ErrDeductionAlreadyPaid):
		writeError(w, http.StatusConflict, err.Error())
	default:
		// FALLS THROUGH to writeMastersError rather than answering 422 flat, so
		// this route agrees with every sibling masters route on what a
		// non-domain failure MEANS: db.ErrNoCompany is a 400 (the request never
		// established a company) and masters.ErrNotFound a 404. Answering 422 for
		// a missing company told a client its well-formed request had failed in
		// processing, which invites a retry that cannot succeed.
		writeMastersError(w, err)
	}
}
