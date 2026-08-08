package server

// auth_payroll_cutoff_test.go — the back-dated cutoff MUST reach the pay run.
//
// ===========================================================================
// WHY THIS FILE EXISTS SEPARATELY FROM auth_test.go
// ===========================================================================
// The back-dated cutoff is the one control an owner has to stop entries being
// posted into a period that is closed and already filed. Every other voucher
// class carries its date on the wire as `voucher_date`, so the PDP peek in
// voucherDateFromRequest finds it and authz.Evaluate applies the cutoff.
//
// The pay run does NOT. POST /vouchers/payroll takes `wage_month` and derives
// the voucher date inside payrolldb (the last day of that month). A PDP that
// only knows how to read `voucher_date` therefore sees no date at all on the
// single most sensitive voucher in the product, and cutoffDeny returns early.
//
// The failure mode is silent and it costs real money: May's PF ECR, ESIC
// monthly return and 24Q have already gone to the portals. A pay run re-posted
// into May writes a second set of ledger entries dated 31 May that appear in no
// filed return, and the next return that reconciles wages to the books will not
// tie.
//
// This test drives the real router, with the real seeded Owner level, against a
// real cutoff set through the real admin API.

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/khata/cored/internal/payroll"
	"github.com/khata/cored/internal/payroll/payrolldb"
)

// aclOwnerLevelID finds the seeded Owner security level for a company.
func aclOwnerLevelID(t *testing.T, s *Server, base, ownerTok string) string {
	t.Helper()
	rec, body := aclRequest(t, s, http.MethodGet, base+"/security-levels", ownerTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /security-levels = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	levels, _ := body["security_levels"].([]any)
	for _, l := range levels {
		m, _ := l.(map[string]any)
		if owner, _ := m["is_owner"].(bool); owner {
			id, _ := m["id"].(string)
			if id == "" {
				t.Fatalf("owner level has no id in %v", body)
			}
			return id
		}
	}
	t.Fatalf("no owner level in %v", body)
	return ""
}

// aclSetCutoff sets (or, with "", clears) a level's back-dated cutoff through
// the admin API — never by writing the column directly, because the point of
// this test is the whole path an owner actually uses.
func aclSetCutoff(t *testing.T, s *Server, base, levelID, ownerTok, cutoff string) {
	t.Helper()
	rec, _ := aclRequest(t, s, http.MethodPut, base+"/security-levels/"+levelID, ownerTok,
		map[string]any{"backdated_cutoff": cutoff})
	if rec.Code != http.StatusOK {
		t.Fatalf("set backdated_cutoff=%q = %d, want 200 (%s)", cutoff, rec.Code, rec.Body.String())
	}
}

// aclVoucherDate reads back the stored voucher date for a posted voucher, so a
// failure message can name the date that actually landed in the ledger rather
// than only the status code that let it in.
func aclVoucherDate(t *testing.T, s *Server, base, voucherID, ownerTok string) string {
	t.Helper()
	rec, body := aclRequest(t, s, http.MethodGet, base+"/vouchers/"+voucherID, ownerTok, nil)
	if rec.Code != http.StatusOK {
		return "(unreadable: HTTP " + http.StatusText(rec.Code) + ")"
	}
	d, _ := body["voucher_date"].(string)
	return d
}

// TestBackDatedCutoffRefusesAPayRunIntoAClosedWageMonth is the gate.
//
// The company's Owner level is given a cutoff of 2026-07-01 — the ordinary
// thing an owner does the day the previous quarter's returns are filed. Then:
//
//	CONTROL  POST /vouchers/journal  voucher_date 2026-05-31 -> 403
//	         (proves the cutoff is live on this company at all; without it a
//	         403 below could be any other denial and this test would be worth
//	         nothing)
//	ATTACK   POST /vouchers/payroll  wage_month  2026-05     -> MUST be 403
//	         (the derived voucher date is 2026-05-31, before the cutoff)
//	ALLOWED  POST /vouchers/payroll  wage_month  2026-08     -> MUST be 201
//	         (the derived voucher date is 2026-08-31, after the cutoff; without
//	         this leg a fix that refuses EVERY pay run would pass)
//	MALFORMED POST /vouchers/payroll wage_month  "" / junk   -> MUST NOT be 201
//	         (an unparseable month must not be a way to skip the cutoff)
func TestBackDatedCutoffRefusesAPayRunIntoAClosedWageMonth(t *testing.T) {
	s := payrollServer(t)
	f := newACLFixture(t, s)

	levelID := aclOwnerLevelID(t, s, f.base, f.ownerTok)
	const cutoff = "2026-07-01"
	aclSetCutoff(t, s, f.base, levelID, f.ownerTok, cutoff)

	// CONTROL. An ordinary journal dated before the cutoff must be refused.
	// This is what makes every assertion below attributable to the cutoff.
	rec, _ := aclRequest(t, s, http.MethodPost, f.base+"/vouchers/journal", f.ownerTok,
		map[string]any{"voucher_date": "2026-05-31", "postings": []any{}})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("CONTROL: POST /vouchers/journal dated 2026-05-31 under a %s cutoff = %d, want 403 — "+
			"the cutoff is not live on this company, so the payroll assertions below prove nothing (%s)",
			cutoff, rec.Code, rec.Body.String())
	}

	// ATTACK. The pay run carries no voucher_date on the wire; its date is the
	// last day of wage_month. May 2026 is closed.
	rec, body := aclRequest(t, s, http.MethodPost, f.base+"/vouchers/payroll", f.ownerTok,
		map[string]any{"wage_month": "2026-05", "created_by": "cutoff-test", "narration": "May re-run"})
	if rec.Code != http.StatusForbidden {
		landed := ""
		if vid, _ := body["voucher_id"].(string); vid != "" {
			landed = " The voucher landed dated " + aclVoucherDate(t, s, f.base, vid, f.ownerTok) + "."
		}
		t.Fatalf("POST /vouchers/payroll wage_month=2026-05 under a %s cutoff = %d, want 403 — "+
			"a payroll level posted salaries dated 2026-05-31 into a period closed at %s. May's PF ECR, "+
			"ESIC monthly return and 24Q are already filed and these ledger entries appear in no filed "+
			"return; the next reconciliation of wages to the books will not tie.%s The cutoff is applied "+
			"from voucherDateFromRequest, which must derive the pay run's date from wage_month — the pay "+
			"run has no voucher_date field on the wire and never should (%s)",
			cutoff, rec.Code, cutoff, landed, rec.Body.String())
	}
	// The reason must be the CUTOFF's, and it must NAME THE BOUNDARY. It is
	// checked as a prefix plus the date rather than by equality because
	// authz.cutoffDeny appends the cutoff it judged against ("...cutoff of
	// 2026-07-01"): a refusal that says only that some period is locked leaves the
	// operator to find the boundary by resubmitting with earlier dates. Equality
	// here would have to be re-pinned every time the sentence is improved, and
	// what this test actually cares about is WHICH control refused.
	msg, _ := body["error"].(string)
	if !strings.HasPrefix(msg, "voucher date is before the back-dated cutoff") {
		t.Errorf("refusal reason = %q, want the cutoff reason — a 403 for some other reason (a missing "+
			"grant, a step-up) would mean the cutoff still does not reach the pay run (%s)",
			msg, rec.Body.String())
	}
	if !strings.Contains(msg, cutoff) {
		t.Errorf("refusal reason = %q, want it to name the cutoff date %s — an operator told only that a "+
			"period is locked cannot tell WHERE the lock starts, and a pay run is exactly the write where "+
			"the date was never typed by hand (%s)", msg, cutoff, rec.Body.String())
	}

	// ALLOWED. August 2026 is after the cutoff and must still post, or the fix
	// is a blanket refusal of payroll and nobody can run August's salaries.
	rec, body = aclRequest(t, s, http.MethodPost, f.base+"/vouchers/payroll", f.ownerTok,
		map[string]any{"wage_month": "2026-08", "created_by": "cutoff-test", "narration": "August run"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /vouchers/payroll wage_month=2026-08 under a %s cutoff = %d, want 201 — August is "+
			"AFTER the cutoff, so refusing it means the cutoff derivation blocks every pay run and the "+
			"business cannot pay its staff (%s)", cutoff, rec.Code, rec.Body.String())
	}
	if got, _ := body["status"].(string); got != "POSTED" {
		t.Errorf("August pay run status = %q, want POSTED (%s)", got, rec.Body.String())
	}

	// MALFORMED. An absent or unparseable wage_month must NOT be a hole in the
	// cutoff. This leg is a BACKSTOP and is honestly weak on its own: today
	// handlePayrollRun's own parseWageMonth 400s every one of these, so it holds
	// whichever direction the PDP peek chooses. It is here so that a future
	// change which loosens the handler cannot silently open the hole.
	// TestMalformedWageMonthRefusesRatherThanSkippingTheCutoff below is the leg
	// that actually pins the PDP's choice.
	for _, bad := range []any{"", "2026-13", "May 2026", nil} {
		rec, _ := aclRequest(t, s, http.MethodPost, f.base+"/vouchers/payroll", f.ownerTok,
			map[string]any{"wage_month": bad, "created_by": "cutoff-test"})
		if rec.Code == http.StatusCreated {
			t.Errorf("POST /vouchers/payroll wage_month=%v = 201 — a malformed wage month posted a pay "+
				"run, which means an unparseable month is a way to walk straight through the back-dated "+
				"cutoff (%s)", bad, rec.Body.String())
		}
	}
}

// TestPayRunCutoffDateIsThePostingsOwnRule proves the PDP and the posting path
// derive the SAME date from the SAME wage month.
//
// This is the failure the project has already been bitten by twice — two leg
// builders, two clause sets — arriving a third time. If the peek grew its own
// copy of "last day of the month" it could differ from payrolldb's by a day at
// a month boundary, and then the cutoff would judge 30 April while the ledger
// stored 31 May (or the reverse). Nothing in the system compares those two
// dates, so the disagreement would only ever surface as a filed return that
// does not tie. The test calls payrolldb.LastDayOf directly, so it fails the
// moment the peek stops calling it.
func TestPayRunCutoffDateIsThePostingsOwnRule(t *testing.T) {
	for _, month := range []string{"2026-01", "2026-02", "2024-02", "2026-04", "2026-12"} {
		body := []byte(`{"wage_month":"` + month + `","created_by":"x"}`)
		got := peekPayrollVoucherDate(body)
		if got == nil {
			t.Fatalf("peekPayrollVoucherDate(%q) = nil — a well-formed wage month yielded no date, so "+
				"the cutoff does not apply to that pay run at all", month)
		}
		m, err := parseWageMonth(month)
		if err != nil {
			t.Fatalf("parseWageMonth(%q): %v", month, err)
		}
		want := payrolldb.LastDayOf(m.First())
		if !got.Equal(want) {
			t.Errorf("wage_month %s: the cutoff would judge %s but the ledger stores %s — the PDP and "+
				"payrolldb no longer agree on what day a pay run is dated, so a run can be authorized "+
				"against one period and posted into another",
				month, got.Format("2006-01-02"), want.Format("2006-01-02"))
		}
		// And the rule itself is the wage month's last day, independently stated.
		if wantLast := (payroll.WageMonth{Year: m.Year, Month: m.Month}).Last(); !want.Equal(wantLast) {
			t.Errorf("payrolldb.LastDayOf(%s) = %s, want the wage month's last day %s",
				month, want.Format("2006-01-02"), wantLast.Format("2006-01-02"))
		}
	}
}

// TestMalformedWageMonthRefusesRatherThanSkippingTheCutoff pins the SAFE
// DIRECTION for a pay run whose wage month cannot be read.
//
// There are two possible answers and only one of them is safe. Returning nil
// means "no date", and cutoffDeny SKIPS when the date is nil — so nil would
// make `{"wage_month":"notamonth"}` a way to present a pay run to the PDP with
// the cutoff disabled. Authorization runs before the handler, so the handler's
// own 400 for that month is not what protects the closed period; it is only
// what happens afterwards, if the request survives.
//
// The safe answer is a date before every cutoff an administrator could set.
func TestMalformedWageMonthRefusesRatherThanSkippingTheCutoff(t *testing.T) {
	// A cutoff at the earliest date the admin API will accept a company using.
	// Anything the peek returns for a malformed month must be before it.
	earliestPlausibleCutoff := time.Date(1900, time.January, 1, 0, 0, 0, 0, time.UTC)

	for _, body := range []string{
		`{"created_by":"x"}`,                // wage_month absent
		`{"wage_month":"","created_by":""}`, // wage_month empty
		`{"wage_month":"2026-13"}`,          // month 13
		`{"wage_month":"May 2026"}`,         // not YYYY-MM
		`{"wage_month":123}`,                // wrong JSON type: the probe fails to unmarshal
		`not json at all`,                   // not a JSON document
	} {
		got := peekPayrollVoucherDate([]byte(body))
		if got == nil {
			t.Errorf("peekPayrollVoucherDate(%s) = nil — cutoffDeny skips a nil date, so a pay run whose "+
				"wage month cannot be read is presented to the PDP with the back-dated cutoff DISABLED. "+
				"An unreadable month must refuse, not be waved through into a period that is closed and "+
				"already filed", body)
			continue
		}
		if !got.Before(earliestPlausibleCutoff) {
			t.Errorf("peekPayrollVoucherDate(%s) = %s, want a date before every settable cutoff (%s) — an "+
				"unreadable wage month must be refused by cutoffDeny, and a date this late is not",
				body, got.Format("2006-01-02"), earliestPlausibleCutoff.Format("2006-01-02"))
		}
	}

	// The counterpart: a WELL-FORMED month must NOT be swept up by the refusal,
	// or every pay run is blocked at any company that has ever set a cutoff.
	if got := peekPayrollVoucherDate([]byte(`{"wage_month":"2026-08"}`)); got == nil ||
		got.Before(earliestPlausibleCutoff) {
		t.Fatalf("peekPayrollVoucherDate(2026-08) = %v, want 2026-08-31 — a readable month was treated as "+
			"unreadable, which refuses every pay run at any company with a cutoff set", got)
	}
}
