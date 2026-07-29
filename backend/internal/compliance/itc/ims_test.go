package itc

import (
	"errors"
	"testing"
	"time"
)

// TestIMSApplies pins the section carve-out. Getting it backwards produces the
// worst available failure: a filer "rejects" an RCM invoice in the belief that
// it removes the credit, when in law they owe the tax and hold the credit
// regardless.
func TestIMSApplies(t *testing.T) {
	in := []string{SectionB2B, SectionB2BA, SectionCDNR, SectionCDNRA, SectionECOM, SectionECOMA}
	out := []string{SectionIMPG, SectionIMPGSEZ, SectionISD, SectionISDA}

	for _, s := range in {
		if !IMSApplies(s, false) {
			t.Fatalf("IMS must apply to section %s", s)
		}
		// ...but NEVER to a reverse-charge row, whatever section it arrives in.
		// RCM supplies DO stay in GSTR-2B (FAQ 18), so they DO arrive in B2B.
		if IMSApplies(s, true) {
			t.Fatalf("IMS applied to a REVERSE-CHARGE row in section %s. GSTN IMS FAQ 8/18: "+
				"RCM invoices are not part of IMS but continue to be part of GSTR-2B", s)
		}
	}
	for _, s := range out {
		if IMSApplies(s, false) {
			t.Fatalf("IMS must NOT apply to section %s — imports and ISD credit do not "+
				"arrive through the supplier-filed path IMS governs", s)
		}
	}
}

// TestValidateAction covers the four gates in the order a human would apply
// them, and each failure names the consequence rather than the rule.
func TestValidateAction(t *testing.T) {
	oct := Period{2025, time.October}
	sep := Period{2025, time.September}

	t.Run("unknown_action_is_refused", func(t *testing.T) {
		err := ValidateAction("PARK", SectionB2B, false, oct, PendingOrdinary, false)
		if !errors.Is(err, ErrUnknownAction) {
			t.Fatalf("got %v, want ErrUnknownAction", err)
		}
	})

	t.Run("action_on_an_import_row_is_ErrIMSNotApplicable", func(t *testing.T) {
		err := ValidateAction(ActionAccept, SectionIMPG, false, oct, PendingOrdinary, false)
		if !errors.Is(err, ErrIMSNotApplicable) {
			t.Fatalf("got %v, want ErrIMSNotApplicable — recording an ACCEPT against an "+
				"import bill of entry makes the action log claim the filer confirmed "+
				"something the portal never asked about", err)
		}
	})

	t.Run("action_on_an_rcm_row_is_ErrIMSNotApplicable", func(t *testing.T) {
		err := ValidateAction(ActionReject, SectionB2B, true, oct, PendingOrdinary, false)
		if !errors.Is(err, ErrIMSNotApplicable) {
			t.Fatalf("got %v, want ErrIMSNotApplicable", err)
		}
	})

	t.Run("action_after_the_return_is_filed_is_refused", func(t *testing.T) {
		err := ValidateAction(ActionAccept, SectionB2B, false, oct, PendingOrdinary, true)
		if !errors.Is(err, ErrReturnFiled) {
			t.Fatalf("got %v, want ErrReturnFiled — GSTN IMS FAQ 12 freezes the action set "+
				"at GSTR-3B filing, so accepting one here makes this software's log "+
				"disagree with the portal's", err)
		}
	})

	t.Run("pending_on_a_credit_note_before_oct_2025_is_refused", func(t *testing.T) {
		err := ValidateAction(ActionPending, SectionCDNR, false, sep, PendingOriginalCreditNote, false)
		if !errors.Is(err, ErrPendingNotAllowed) {
			t.Fatalf("got %v, want ErrPendingNotAllowed for the Sep-2025 tax period", err)
		}
	})

	t.Run("pending_on_a_credit_note_from_oct_2025_is_allowed", func(t *testing.T) {
		if err := ValidateAction(ActionPending, SectionCDNR, false, oct, PendingOriginalCreditNote, false); err != nil {
			t.Fatalf("got %v, want nil — a hard-coded \"PENDING is never allowed on a "+
				"credit note\" is WRONG for every period this software ships into "+
				"[CITED-SECONDARY: GSTN Advisory 631 of 08-10-2025, VERIFY BEFORE RELEASE]", err)
		}
	})

	t.Run("ordinary_actions_pass", func(t *testing.T) {
		for _, k := range []string{ActionAccept, ActionReject, ActionPending, ActionReset} {
			if err := ValidateAction(k, SectionB2B, false, oct, PendingOrdinary, false); err != nil {
				t.Fatalf("%s on an ordinary B2B row: %v", k, err)
			}
		}
	})
}
