package server

// HTTP surface for T5, the e-way bill.
//
// ===========================================================================
// ⛔⛔ THE ROUTE IS FLAT (/e-way-bills) AND NOT NESTED UNDER /vouchers, AND
// THAT IS FORCED RATHER THAN CHOSEN
// ===========================================================================
// HANDOFF §4.2 sketched `POST .../vouchers/{voucherID}/e-way-bill`. That path
// CANNOT WORK in cored: routeAction's voucher arm is
// `method == POST && strings.HasPrefix(rest, "/vouchers/")`, and it reads
// everything after the prefix as the voucher CLASS. A POST to
// /vouchers/{id}/e-way-bill would be mapped as a request to create a voucher of
// class "{id}/e-way-bill" — a class no vocabulary knows, i.e. default-deny for
// the Owner too, and the failure would present as an unexplained 403.
//
// So the resource lives at its own top-level path and names the voucher in the
// body (POST) or the path (GET). The frontend contract already assumed this:
// EWayBillRequest in api/types.ts carries `voucher_id`.
//
// ===========================================================================
// ONE BARE RESOURCE TOKEN, `e-way-bill`, AND NO ACL MIGRATION
// ===========================================================================
// HANDOFF §6.1: Data Entry's default_access is 'none' and every seeded rule
// names a specific token, so a token never mentioned is DENIED. State the
// consequence plainly, as the T3 arm does: DATA ENTRY CANNOT RAISE OR SEE AN
// e-WAY BILL until an administrator writes `allow create e-way-bill`.
//
// ⛔ `e-way-bill` IS NOT IN authz.KnownReportNames AND MUST NOT BE ADDED.
// server.go loops that list and auto-mounts GET /print/reports/<name>; a name
// with no reportName() case is default-DENY for everyone including the Owner,
// which already shipped once for reconciliation-queue. There is no e-way bill
// REPORT in this slice. The day somebody adds a register, it is a report name,
// it needs a reportName() case, a REPORT_SLUGS entry, and — mandatorily — an
// ACL migration writing BOTH `disallow display` AND `disallow print` in the
// same commit (§6.1, §6.2).

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/khata/cored/internal/ewaybill"
	"github.com/khata/cored/internal/statutory/rules"
)

// eWayBillReq is the body of POST .../e-way-bills. It mirrors EWayBillRequest in
// frontend/src/api/types.ts field for field.
type eWayBillReq struct {
	VoucherID string `json:"voucher_id"`

	TransporterID   string `json:"transporter_id"`
	TransporterName string `json:"transporter_name"`
	VehicleNumber   string `json:"vehicle_number"`
	TransportMode   string `json:"transport_mode"`

	// DistanceKm is a NUMBER on the wire and an int here — it is a distance, not
	// money, so it carries no paisa and the fixed-4 string convention does not
	// apply to it. ⚠️ Zero means NOT STATED and is refused with the assessment's
	// own reasoning; it is never read as a zero-distance movement.
	DistanceKm int `json:"distance_km"`

	// ⛔ NO DEFAULT ANYWHERE ON THIS PATH. Absent means false means ordinary
	// cargo, and the assessment SAYS SO in its basis rather than leaving it
	// implicit — because assuming ordinary grants ten times the validity the rule
	// allows.
	OverDimensionalCargo bool `json:"over_dimensional_cargo"`

	// Route overrides. Empty means "use the company / party master address".
	FromPincode   string `json:"from_pincode"`
	FromStateCode string `json:"from_state_code"`
	ToPincode     string `json:"to_pincode"`
	ToStateCode   string `json:"to_state_code"`

	CreatedBy string `json:"created_by"`
}

// eWayBillJSON is the wire shape of a raised bill. It matches EWayBillResponse
// in frontend/src/api/types.ts.
//
// ⛔ `local_reference` AND NEVER `ebn` / `e_way_bill_no` / `number`. See
// migration 0047's header: a document carrying a fabricated EBN tells a
// checkpost the consignment is covered when it is not.
func eWayBillJSON(v ewaybill.View) map[string]any {
	return map[string]any{
		"voucher_id":   v.VoucherID.String(),
		"voucher_no":   v.VoucherNo,
		"voucher_date": v.VoucherDate,

		"local_reference": v.LocalReference,
		"provider":        v.Provider,
		"payload":         v.Payload,
		"disclosures":     v.Disclosures,

		"valid_until":   v.ValidUntil,
		"validity_days": v.ValidityDays,

		"assessed_required": v.AssessedRequired,
		"part_b_required":   v.PartBRequired,
		"blockers":          nonNilStrings(v.Blockers),

		"transporter_id":         v.TransporterID,
		"transporter_name":       v.TransporterName,
		"vehicle_number":         v.VehicleNumber,
		"transport_mode":         v.TransportMode,
		"distance_km":            v.DistanceKm,
		"over_dimensional_cargo": v.OverDimensionalCargo,
		"from_pincode":           v.FromPincode,
		"to_pincode":             v.ToPincode,
		"from_state_code":        v.FromStateCode,
		"to_state_code":          v.ToStateCode,

		"generation_count": v.GenerationCount,
		"generated_at":     v.GeneratedAt.Format(time.RFC3339),
		"stale":            v.Stale,
	}
}

// (nonNilStrings lives in handler_bank_statement.go and is reused here rather
// than copied: it renders a nil slice as [] instead of null, which matters most
// for `blockers` — the happy path is the one where it is empty, so a client that
// crashes on null crashes only when nothing is wrong.)

// postEWayBill raises (or re-raises) the e-way bill for one voucher.
func (s *Server) postEWayBill() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, ok := s.scopeCtx(w, r)
		if !ok {
			return
		}
		var req eWayBillReq
		if !decodeJSON(w, r, &req) {
			return
		}

		voucherID, err := optionalUUID(req.VoucherID)
		if err != nil || voucherID == uuid.Nil {
			// ⛔ The all-zeros UUID PARSES (HANDOFF §9.5), so `err == nil` is not
			// enough — an explicit 00000000-… would otherwise become a lookup that
			// finds nothing and reads to the operator as "that voucher is gone".
			writeError(w, http.StatusBadRequest,
				"voucher_id must be the uuid of the voucher the goods move against")
			return
		}

		mode := rules.TransportMode(strings.ToUpper(strings.TrimSpace(req.TransportMode)))
		if mode != "" && !mode.Valid() {
			// Named rather than enumerated from a literal list here: TransportMode.Valid
			// is the one place the four modes live, and restating them in this message
			// would be a second copy to drift.
			writeError(w, http.StatusBadRequest,
				"transport_mode must be one of ROAD, RAIL, AIR or SHIP")
			return
		}

		view, err := s.ewaybill.Raise(ctx, ewaybill.Input{
			VoucherID:            voucherID,
			TransporterID:        strings.TrimSpace(req.TransporterID),
			TransporterName:      strings.TrimSpace(req.TransporterName),
			VehicleNumber:        strings.TrimSpace(req.VehicleNumber),
			Mode:                 mode,
			DistanceKm:           req.DistanceKm,
			OverDimensionalCargo: req.OverDimensionalCargo,
			FromPincode:          strings.TrimSpace(req.FromPincode),
			FromStateCode:        strings.TrimSpace(req.FromStateCode),
			ToPincode:            strings.TrimSpace(req.ToPincode),
			ToStateCode:          strings.TrimSpace(req.ToStateCode),
			CreatedBy:            strings.TrimSpace(req.CreatedBy),
			// The clock is read HERE, at the edge, and passed in — the domain never
			// reads it. That is what makes the validity reproducible and the
			// assessment testable at a pinned instant.
			GeneratedAt: time.Now().UTC(),
		})
		if err != nil {
			writeEWayBillError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, eWayBillJSON(view))
	}
}

// getEWayBill returns the stored e-way bill for one voucher.
func (s *Server) getEWayBill() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, ok := s.scopeCtx(w, r)
		if !ok {
			return
		}
		voucherID, err := optionalUUID(chi.URLParam(r, "voucherID"))
		if err != nil || voucherID == uuid.Nil {
			writeError(w, http.StatusBadRequest, "voucherID must be a uuid")
			return
		}
		view, err := s.ewaybill.Get(ctx, voucherID)
		if err != nil {
			writeEWayBillError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, eWayBillJSON(view))
	}
}

// writeEWayBillError maps the domain's sentinels onto status codes.
//
// ⛔ EVERY REFUSAL CARRIES ITS REASONING TO THE CLIENT, and for the two
// statutory ones that means a LIST rather than a sentence. Assess and
// ValidateEWayBill both return every problem instead of the first, deliberately,
// so that an operator fixing a consignment gets one round of corrections rather
// than five. Flattening them into a single string here would throw that away at
// the last hop.
func writeEWayBillError(w http.ResponseWriter, err error) {
	var notAssessable *ewaybill.ErrNotAssessable
	var schema *ewaybill.ErrSchemaViolation

	switch {
	case errors.Is(err, ewaybill.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ewaybill.ErrNoGoods),
		errors.Is(err, ewaybill.ErrNoTaxLines),
		errors.Is(err, ewaybill.ErrLineMismatch):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	case errors.As(err, &notAssessable):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error":    notAssessable.Error(),
			"blockers": nonNilStrings(notAssessable.Blockers),
			// The BASIS travels too. A refusal that says "no distance was stated"
			// without the reasoning is a refusal an operator argues with; one that
			// carries Assess's own explanation of why a guessed validity is
			// dangerous is one they act on.
			"basis": nonNilStrings(notAssessable.Basis),
		})
	case errors.As(err, &schema):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error":      schema.Error(),
			"violations": nonNilStrings(schema.Violations),
		})
	default:
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	}
}
