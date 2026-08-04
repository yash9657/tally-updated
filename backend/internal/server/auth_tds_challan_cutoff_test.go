package server

// auth_tds_challan_cutoff_test.go — THE BACK-DATED CUTOFF MUST REACH A DEPOSIT.
//
// ===========================================================================
// WHY THIS FILE EXISTS, AND WHY IT IS THE SECOND TIME
// ===========================================================================
// The back-dated cutoff is the one control an owner has to stop entries being
// asserted into a period that is closed and already filed. It is applied from
// voucherDateFromRequest, which peeks the write body for a date — and it only
// knew two shapes: `voucher_date`, which every voucher class carries, and
// `wage_month`, which the pay run carries instead.
//
// A TDS CHALLAN CARRIES NEITHER. It dates itself with `deposit_date`. So the PDP
// saw no date at all, cutoffDeny returned early (it skips entirely when the date
// is nil), and a deposit could be recorded into a locked period.
//
// ⛔ AND THE ROUTE SHIPPED WITH A COMMENT SAYING THE OPPOSITE: "recording a
// deposit dated inside a closed period IS a back-dated act", written next to a
// control that never ran. That is worse than the missing control, because the
// next reader has been told to stop looking. It is the same defect the pay run
// had, found the same way — by asking whether the claim was true rather than
// whether it was plausible — and auth_payroll_cutoff_test.go's header predicted
// the class exactly.
//
// WHY IT MATTERS FOR A DEPOSIT SPECIFICALLY. A challan is an assertion that
// money reached the Government on a date. Once a quarter's return is filed, the
// deposits it claimed are fixed and reconciled against the department's own .csi
// challan file. A challan inserted into that closed quarter afterwards changes
// what the books claim was deposited without changing what was filed, so the
// register and the filed return stop agreeing — and the discrepancy surfaces at
// the portal, not here.
//
// Recording a genuinely old deposit is a real need. The answer is to move the
// cutoff deliberately, by whoever owns the lock, which is the conversation the
// cutoff exists to force.

import (
	"net/http"
	"testing"
)

// TestAChallanDepositedIntoAClosedPeriodIsRefused drives the real router with a
// real cutoff set through the real admin API.
//
// The legs, and why each one is load-bearing:
//
//	CONTROL   POST /vouchers/journal  voucher_date 2026-05-31 -> 403
//	          Proves the cutoff is live on THIS company. Without it, the 403
//	          below could be any other denial and this test would be worthless.
//	ATTACK    POST /tds-challans      deposit_date 2026-05-31 -> MUST be 403
//	          The date the cutoff must read is deposit_date.
//	ALLOWED   POST /tds-challans      deposit_date 2026-06-07 -> MUST be 201
//	          Without this leg, a "fix" that refused EVERY challan would pass —
//	          and refusing every deposit breaks the whole feature.
func TestAChallanDepositedIntoAClosedPeriodIsRefused(t *testing.T) {
	s := payrollServer(t)
	f := newTDSChallanFixture(t, s, "cutoff")

	levelID := aclOwnerLevelID(t, s, f.base, f.ownerTok)
	// The ordinary thing an owner does the day the previous quarter is filed.
	// It is AFTER the fixture's deduction (2026-05-10) and BEFORE the fixture's
	// ordinary deposit date (2026-06-07), so both legs below are meaningful.
	const cutoff = "2026-06-01"
	aclSetCutoff(t, s, f.base, levelID, f.ownerTok, cutoff)

	// CONTROL.
	rec, _ := aclRequest(t, s, http.MethodPost, f.base+"/vouchers/journal", f.ownerTok,
		map[string]any{"voucher_date": "2026-05-31", "postings": []any{}})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("CONTROL: POST /vouchers/journal dated 2026-05-31 under a %s cutoff = %d, want 403 — "+
			"the cutoff is not live on this company, so the challan assertions below prove nothing (%s)",
			cutoff, rec.Code, rec.Body.String())
	}

	// ATTACK. Same valid challan, dated into the closed period.
	body := f.challanBody()
	body["deposit_date"] = "2026-05-31"
	rec, _ = aclRequest(t, s, http.MethodPost, f.base+"/tds-challans", f.ownerTok, body)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST /tds-challans with deposit_date=2026-05-31 under a %s cutoff = %d, want 403 — "+
			"a deposit was asserted into a period the owner has locked and whose return is filed. The "+
			"cutoff is applied from voucherDateFromRequest, which must read deposit_date for this route "+
			"(see peekDepositDate); reading only voucher_date leaves the PDP with no date and cutoffDeny "+
			"skips entirely (%s)", cutoff, rec.Code, rec.Body.String())
	}

	// ALLOWED. The same challan on its own date, after the cutoff, must still
	// work — otherwise the control has eaten the feature.
	rec, _ = aclRequest(t, s, http.MethodPost, f.base+"/tds-challans", f.ownerTok, f.challanBody())
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /tds-challans with deposit_date=2026-06-07 under a %s cutoff = %d, want 201 — "+
			"a deposit AFTER the cutoff must be recordable; a cutoff that refuses every challan would "+
			"pass the attack leg above and still be broken (%s)", cutoff, rec.Code, rec.Body.String())
	}
}
