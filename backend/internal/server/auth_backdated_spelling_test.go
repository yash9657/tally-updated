package server

// auth_backdated_spelling_test.go — THE BACK-DATED CUTOFF MUST NOT BE
// BYPASSABLE BY LOOSENING THE SPELLING OF THE DATE.
//
// ===========================================================================
// WHY THIS FILE EXISTS, AND WHY IT IS THE THIRD TIME
// ===========================================================================
// The cutoff was inert on the pay run (auth_payroll_cutoff_test.go) and then on
// the TDS challan (auth_tds_challan_cutoff_test.go), both times because the PDP
// peek could not FIND the date. This is the same control failing for the
// adjacent reason: the peek found the date and could not READ it.
//
// peekVoucherDate parsed with exactly "2006-01-02" and returned nil on failure,
// and authz.cutoffDeny SKIPS ENTIRELY when the date is nil. Meanwhile the very
// same string travels to Postgres unparsed — ledger.Post writes
// `nullIfEmpty(in.VoucherDate)` into a DATE column — and Postgres accepts
// "2026-5-31", "2026-05-31T00:00:00Z" and "20260531" as 31 May 2026. So the two
// layers disagreed about what a date IS, and the gap between them was a door:
// a voucher the PDP could not date was a voucher the ledger dated perfectly,
// inside a period the owner had locked.
//
// WHAT IT COSTS. A cutoff is set the day a quarter's returns go to the portals.
// A voucher inserted into that quarter afterwards changes what the books say
// about a period whose GSTR-1, GSTR-3B and 26Q are already filed, so the books
// and the filed returns stop agreeing — and the discrepancy surfaces at the
// department, not here. Unlike the pay run, this one needs no unusual route: it
// is every voucher class, on the ordinary POST the F-key screens use.
//
// The safe direction is the one the pay run already took: a date that cannot be
// read returns beforeEveryCutoff(), so cutoffDeny refuses instead of skipping.

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// spellingsPostgresReadsAsMay31 are voucher_date spellings that Go's strict
// "2006-01-02" layout REJECTS. The first is the one this test proves lands a
// voucher when the peek fails open: Postgres reads "2026-5-31" as 31 May 2026.
// The rest are the same class of loosening — a client library that formats a
// date its own way, a copy-paste out of a spreadsheet, or somebody probing.
var spellingsPostgresReadsAsMay31 = []string{
	"2026-5-31",            // no zero-padding — accepted by Postgres, refused by time.Parse
	"2026-05-31T00:00:00Z", // RFC 3339: a DATE column truncates the time part
	"20260531",             // ISO 8601 basic form
	"31 May 2026",          // spelled out
	" 2026-05-31",          // leading space
	"2026-05-31 ",          // trailing space
}

// TestBackDatedCutoffIsNotBypassableByRespellingTheVoucherDate drives the real
// router, with the real seeded Owner level, against a real cutoff set through
// the real admin API.
//
// The legs, and why each is load-bearing:
//
//	CONTROL   POST /vouchers/journal  voucher_date 2026-05-31    -> 403
//	          The correctly-spelled back-date. Proves the cutoff is live on THIS
//	          company; without it every 403 below could be any other denial and
//	          the test would be worth nothing.
//	ATTACK    POST /vouchers/journal  voucher_date 2026-5-31 &c. -> MUST be 403
//	          The same date, respelled. This is the bypass.
//	ALLOWED   POST /vouchers/journal  voucher_date 2026-08-15    -> MUST be 201
//	          ⛔ THE HALF THAT IS USUALLY MISSING. A "fix" that refused every
//	          voucher would pass both legs above and stop the business trading.
func TestBackDatedCutoffIsNotBypassableByRespellingTheVoucherDate(t *testing.T) {
	s := payrollServer(t)
	f := newSpellingFixture(t, s)

	levelID := aclOwnerLevelID(t, s, f.base, f.ownerTok)
	// The ordinary thing an owner does the day the previous quarter is filed.
	const cutoff = "2026-07-01"
	aclSetCutoff(t, s, f.base, levelID, f.ownerTok, cutoff)

	// ------------------------------------------------------------------ CONTROL
	rec, body := aclRequest(t, s, http.MethodPost, f.base+"/vouchers/journal", f.ownerTok,
		f.journal("2026-05-31", "SPELL-CONTROL"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("CONTROL: POST /vouchers/journal voucher_date=2026-05-31 under a %s cutoff = %d, want "+
			"403 — the cutoff is not live on this company, so every assertion below proves nothing (%s)",
			cutoff, rec.Code, rec.Body.String())
	}
	// And the refusal must NAME the boundary. An operator told only that "a
	// period is locked" has to guess where the lock starts.
	msg, _ := body["error"].(string)
	if !strings.Contains(msg, cutoff) {
		t.Errorf("the refusal reason %q does not name the cutoff date %s — the operator learns that a "+
			"period is locked without learning where the boundary is, and the only way to find out is to "+
			"keep resubmitting with earlier dates (%s)", msg, cutoff, rec.Body.String())
	}

	// ------------------------------------------------------------------- ATTACK
	for i, spelling := range spellingsPostgresReadsAsMay31 {
		rec, body := aclRequest(t, s, http.MethodPost, f.base+"/vouchers/journal", f.ownerTok,
			f.journal(spelling, "SPELL-ATTACK-"+strconv.Itoa(i)))
		if rec.Code == http.StatusForbidden {
			continue
		}
		landed := ""
		if vid, _ := body["voucher_id"].(string); vid != "" {
			landed = " THE VOUCHER LANDED, dated " + aclVoucherDate(t, s, f.base, vid, f.ownerTok) +
				" — inside the lock."
		}
		t.Errorf("ATTACK: POST /vouchers/journal voucher_date=%q under a %s cutoff = %d, want 403 — the "+
			"back-dated write was ALLOWED because the PDP could not read the date it was given. "+
			"peekVoucherDate parses only \"2006-01-02\" and returned nil, and cutoffDeny skips entirely "+
			"on a nil date, while Postgres reads this same string as 31 May 2026 in a DATE column. So the "+
			"one control an owner has over a filed period is switched off from the wire.%s (%s)",
			spelling, cutoff, rec.Code, landed, rec.Body.String())
	}

	// ------------------------------------------------------------------ ALLOWED
	// August is AFTER the cutoff and must still post, or the fix is a blanket
	// refusal and nobody can enter today's vouchers.
	rec, body = aclRequest(t, s, http.MethodPost, f.base+"/vouchers/journal", f.ownerTok,
		f.journal("2026-08-15", "SPELL-ALLOWED"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("ALLOWED: POST /vouchers/journal voucher_date=2026-08-15 under a %s cutoff = %d, want "+
			"201 — a date AFTER the cutoff must still post. Refusing it means the fail-closed peek now "+
			"refuses every voucher and the business cannot trade (%s)", cutoff, rec.Code, rec.Body.String())
	}
	if vid, _ := body["voucher_id"].(string); vid != "" {
		if got := aclVoucherDate(t, s, f.base, vid, f.ownerTok); got != "2026-08-15" {
			t.Errorf("the allowed voucher landed dated %q, want 2026-08-15 — it posted, but not on the "+
				"date that was authorized", got)
		}
	}

	// ------------------------------------------------- ALLOWED, WITH NO CUTOFF
	// Clearing the cutoff must restore the previous behaviour for a loosely
	// spelled date: fail-closed is a CUTOFF control, not a new date validator.
	// (Whether a loose spelling ought to be a 400 at the wire is a separate
	// question — see the note on peekVoucherDate — but it must not become a 403
	// at a company that has locked nothing.)
	aclSetCutoff(t, s, f.base, levelID, f.ownerTok, "")
	rec, _ = aclRequest(t, s, http.MethodPost, f.base+"/vouchers/journal", f.ownerTok,
		f.journal("2026-5-31", "SPELL-NOCUTOFF"))
	if rec.Code == http.StatusForbidden {
		t.Errorf("with the cutoff CLEARED, voucher_date=2026-5-31 = 403 (%s) — beforeEveryCutoff() must "+
			"be inert where no cutoff is set; a company that has locked nothing is now being refused "+
			"writes by the lock", rec.Body.String())
	}
}

// TestPeekVoucherDateFailsClosedOnAnUnreadableDateAndOpenOnAnAbsentOne pins the
// PDP's choice directly, without the HTTP path — the unit-level counterpart of
// the test above, and the one that fails the moment somebody restores the old
// `return nil`.
//
// ⛔ BOTH DIRECTIONS ARE LOAD-BEARING AND THEY ARE OPPOSITE:
//
//   - PRESENT AND UNREADABLE must return a date before every settable cutoff.
//     nil means "the cutoff does not apply", so nil here is the bypass.
//   - ABSENT must return nil. This peek runs on the fall-through for EVERY
//     write in the product, not only voucher writes — creating a ledger, a
//     godown, a unit, uploading a bill — so failing closed on "no voucher_date
//     in this body" would refuse every master create at any company with a
//     cutoff set. That is an outage, not a control.
func TestPeekVoucherDateFailsClosedOnAnUnreadableDateAndOpenOnAnAbsentOne(t *testing.T) {
	// The earliest date a company could plausibly set as a cutoff. Anything the
	// peek returns for an unreadable date must be before it.
	earliestPlausibleCutoff := time.Date(1900, time.January, 1, 0, 0, 0, 0, time.UTC)

	t.Run("present but unreadable refuses", func(t *testing.T) {
		for _, body := range []string{
			`{"voucher_date":"2026-5-31","postings":[]}`, // no zero-padding
			`{"voucher_date":"2026-05-31T00:00:00Z"}`,    // RFC 3339
			`{"voucher_date":"20260531"}`,                // ISO basic
			`{"voucher_date":"31/05/2026"}`,              // dd/mm/yyyy
			`{"voucher_date":"31 May 2026"}`,             // spelled out
			`{"voucher_date":" 2026-05-31 "}`,            // padded
			`{"voucher_date":"2026-02-30"}`,              // a day that does not exist
			`{"voucher_date":"notadate"}`,                // junk
			`{"voucher_date":20260531}`,                  // wrong JSON type: a number
			`{"voucher_date":{"y":2026}}`,                // wrong JSON type: an object
		} {
			got := peekVoucherDate([]byte(body))
			if got == nil {
				t.Errorf("peekVoucherDate(%s) = nil — cutoffDeny SKIPS a nil date, so a write whose "+
					"voucher_date cannot be read is presented to the PDP with the back-dated cutoff "+
					"DISABLED. Postgres accepts several of these spellings for the DATE column, so the "+
					"voucher then lands correctly dated inside the locked period", body)
				continue
			}
			if !got.Before(earliestPlausibleCutoff) {
				t.Errorf("peekVoucherDate(%s) = %s, want a date before every settable cutoff (%s) — an "+
					"unreadable date must be REFUSED by cutoffDeny, and a date this late is not",
					body, got.Format("2006-01-02"), earliestPlausibleCutoff.Format("2006-01-02"))
			}
		}
	})

	t.Run("absent does not refuse", func(t *testing.T) {
		for _, body := range []string{
			`{"name":"HDFC Current A/c","group_name":"Bank Accounts"}`, // a ledger create
			`{"voucher_date":"","postings":[]}`,                        // empty string = no date
			`{"voucher_date":null}`,                                    // explicit null
			`{}`,                                                       // empty object
			`--boundary\r\nContent-Disposition: form-data`,             // a multipart upload
			`not json at all`,                                          // junk body
			// ⛔ `[{"voucher_date":"2026-5-31"}]` USED TO BE ON THIS LIST AND HAS
			// MOVED. It was here because the round-1 peek decoded into a struct, so
			// a top-level JSON array failed the Unmarshal and read as "no date in
			// this body" — and the justification written beside it, that a voucher
			// write "cannot hide in such a body", was the very assumption the
			// re-drive broke: a voucher hid one level down in
			// {"intents":[{"payload":{...}}]} and committed into a locked period.
			// A nested date is now READ, so that body refuses, and the case lives in
			// TestPeekVoucherDateSeesNestedAndXMLVoucherDates
			// (auth_backdated_nested_test.go) as a NESTED case rather than an absent
			// one. Nothing on the list above nests anything: they are the master
			// creates and uploads that must keep working under a cutoff.
		} {
			if got := peekVoucherDate([]byte(body)); got != nil {
				t.Errorf("peekVoucherDate(%s) = %s, want nil — this body carries no voucher_date to "+
					"back-date, and this peek is the fall-through for EVERY write in the product. "+
					"Refusing it locks every master create and every upload out of any company that "+
					"has set a cutoff", body, got.Format("2006-01-02"))
			}
		}
	})

	t.Run("a correctly spelled date is still read", func(t *testing.T) {
		got := peekVoucherDate([]byte(`{"voucher_date":"2026-08-15","postings":[]}`))
		want := time.Date(2026, time.August, 15, 0, 0, 0, 0, time.UTC)
		if got == nil || !got.Equal(want) {
			t.Fatalf("peekVoucherDate(2026-08-15) = %v, want %s — a readable date was treated as "+
				"unreadable, which refuses every voucher at any company with a cutoff set",
				got, want.Format("2006-01-02"))
		}
	})
}

// ---------------------------------------------------------------------------
// Fixture: a company with two ordinary ledgers, enough to post a real balanced
// journal. It is deliberately NOT the payroll fixture — this defect is about the
// ordinary voucher POST that every F-key screen uses.
// ---------------------------------------------------------------------------

type spellingFixture struct {
	base     string
	ownerTok string
	expense  string
	bank     string
}

// journal builds a BALANCED two-posting journal.
//
// ⚠️ IT CARRIES AN EXPLICIT voucher_no ON PURPOSE, AND THAT IS WHAT MAKES THE
// ATTACK LEG HONEST. With voucher_no empty, ledger.Post takes the auto-numbering
// branch, which calls finYearOf(in.VoucherDate) — a STRICT "2006-01-02" parse —
// and a loosely spelled date dies there with a 500 that has nothing to do with
// authorization. The test would then see a non-201 and could be misread as "the
// cutoff held". Supplying the number skips that branch, so the only thing
// standing between a respelled back-date and the ledger is the PDP.
func (f spellingFixture) journal(date, voucherNo string) map[string]any {
	return map[string]any{
		"voucher_date": date,
		"voucher_no":   voucherNo,
		"narration":    "spelling probe " + voucherNo,
		"created_by":   "spelling-test",
		"postings": []any{
			map[string]any{"ledger_id": f.expense, "dr_cr": "D", "amount": "1000.0000"},
			map[string]any{"ledger_id": f.bank, "dr_cr": "C", "amount": "1000.0000"},
		},
	}
}

func newSpellingFixture(t *testing.T, s *Server) spellingFixture {
	t.Helper()
	companyID, ownerTok := aclOwnerCompany(t, s)
	f := spellingFixture{base: "/v1/companies/" + companyID, ownerTok: ownerTok}

	mkLedger := func(name, group string) string {
		t.Helper()
		rec, body := aclRequest(t, s, http.MethodPost, f.base+"/ledgers", ownerTok,
			map[string]any{"name": name, "group_name": group, "created_by": "spelling-test"})
		if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
			t.Fatalf("create ledger %q = %d, want 201 (%s)", name, rec.Code, rec.Body.String())
		}
		id, _ := body["ledger_id"].(string)
		if id == "" {
			t.Fatalf("create ledger %q returned no ledger_id (%s)", name, rec.Body.String())
		}
		return id
	}
	f.expense = mkLedger("Spelling Sundry Expenses", "Indirect Expenses")
	f.bank = mkLedger("Spelling Bank A/c", "Bank Accounts")
	return f
}
