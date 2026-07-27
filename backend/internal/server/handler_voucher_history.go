package server

// handler_voucher_history.go — THE EDIT LOG, over HTTP.
//
//	GET /v1/companies/{companyID}/vouchers/{voucherID}/history
//
// voucher_version has been append-only and BLAKE3 hash-chained since migration
// 0002, and a BEFORE UPDATE OR DELETE trigger has enforced it — but nothing
// could READ it. GET /vouchers/{id} returns the head version only, so the
// MCA-compliance property the design exists for was invisible to every user.
//
// This route returns the whole chain oldest-first AND a VERIFICATION of it. The
// verification is the part that matters: an audit log you cannot check is a
// claim, not evidence. See internal/ledger/history.go for why checking it
// requires re-canonicalising the payload rather than re-reading stored bytes.
//
// It is a Display on `voucher` — the same (action, resource) as fetching the
// voucher itself, so anyone permitted to see a voucher may see how it got that
// way, and a level that cannot see it cannot see its history either. Read-only,
// so it never inherits the back-dated cutoff or the WebAuthn step-up.
//
// A DELETED voucher still has a history, and that is the whole point: the
// tombstone is the most interesting entry an edit log can carry. So unlike
// handleGetVoucher — which 404s a tombstoned head — this reads through it, and
// reserves 404 for a voucher with no versions at all in this company.

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/platform/db"
)

func (s *Server) handleVoucherHistory(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	voucherID, err := uuid.Parse(chi.URLParam(r, "voucherID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid voucher id")
		return
	}
	h, err := s.ledger.VoucherHistory(ctx, voucherID)
	if err != nil {
		switch {
		case errors.Is(err, ledger.ErrVoucherNotFound):
			writeError(w, http.StatusNotFound, "voucher not found")
		case errors.Is(err, db.ErrNoCompany):
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	versions := make([]map[string]any, 0, len(h.Versions))
	for _, v := range h.Versions {
		postings := make([]map[string]any, 0, len(v.Postings))
		for _, p := range v.Postings {
			postings = append(postings, map[string]any{
				"ledger_id": p.LedgerID,
				"dr_cr":     p.DrCr,
				"amount":    p.Amount,
			})
		}
		bills := make([]map[string]any, 0, len(v.BillAllocations))
		for _, b := range v.BillAllocations {
			bills = append(bills, map[string]any{
				"ledger_id": b.LedgerID,
				"ref_name":  b.RefName,
				"ref_type":  b.RefType,
				"dr_cr":     b.DrCr,
				"amount":    b.Amount,
				"due_date":  b.DueDate,
			})
		}
		versions = append(versions, map[string]any{
			"version_no":       v.VersionNo,
			"op":               v.Op,
			"created_by":       v.CreatedBy,
			"created_at":       v.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
			"node_id":          v.NodeID,
			"hash":             v.Hash,
			"prev_hash":        v.PrevHash,
			"class":            v.Class,
			"voucher_no":       v.VoucherNo,
			"voucher_date":     v.VoucherDate,
			"narration":        v.Narration,
			"party":            v.Party,
			"postings":         postings,
			"bill_allocations": bills,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"voucher_id": h.VoucherID.String(),
		"is_deleted": h.IsDeleted,
		"versions":   versions,
		"verification": map[string]any{
			"ok":                h.Verification.OK,
			"versions_checked":  h.Verification.VersionsChecked,
			"broken_at_version": h.Verification.BrokenAtVersion,
			"reason":            h.Verification.Reason,
		},
	})
}
