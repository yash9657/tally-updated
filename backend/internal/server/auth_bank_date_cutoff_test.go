package server

// auth_bank_date_cutoff_test.go — THE BACK-DATED CUTOFF MUST REACH A BANK DATE.
//
// ===========================================================================
// WHY THIS FILE EXISTS, AND WHY IT IS THE THIRD TIME
// ===========================================================================
// A write is judged on the date it carries, and voucherDateFromRequest knew
// three spellings: `voucher_date`, the pay run's `wage_month`, and the challan's
// `deposit_date`. THE BANK RECONCILIATION CARRIES A FOURTH — `bank_date` — so
// the PDP saw no date at all and cutoffDeny skipped entirely.
//
// ⛔ AND, FOR THE SECOND TIME IN THIS CLASS, THE ROUTE SHIPPED WITH A COMMENT
// SAYING THE CONTROL WAS IN PLACE: routeAction's bank-reconciliation arm said the
// write "correctly inherits the back-dated cutoff — a bank date asserted into a
// closed period is exactly the kind of back-dating that cutoff exists for". A
// comment asserting a control that never runs is worse than a missing control,
// because the next reader has been told to stop looking. This one was found by an
// audit that drove the product in a browser and then asked whether each claim was
// TRUE rather than whether it was plausible.
//
// WHY IT MATTERS FOR A BANK DATE SPECIFICALLY. Reconciling moves no ledger figure
// (invariant I-2), so this is not about a balance. It is about what the books
// ASSERT: a bank date is a statement that money actually moved through the
// account on that day. Once a period is closed the reconciled position is what
// the audited cash and bank figures rest on, and re-dating or withdrawing a
// clearance inside it changes that position after the fact, silently, with the
// statement it was copied from long since filed away.
//
// ===========================================================================
// THE DOORS THIS FILE WALKS, INCLUDING THE ONE LEFT DELIBERATELY OPEN
// ===========================================================================
//
//	ASSERT INTO the lock   PUT  bank_date inside the closed period    -> 403
//	MOVE OUT of the lock   PUT  a post-cutoff date onto a leg cleared
//	                            inside it (the PUT is an UPSERT)      -> 403
//	WITHDRAW from the lock DELETE a leg cleared inside it             -> 403
//	LOOSE SPELLING         PUT  bank_date "2026-5-20"                 -> 403
//	                            (⛔ Postgres accepts what Go's layout rejects —
//	                            unlike the challan, whose store parses strictly)
//	TRAILING BYTES         PUT  a perfect body + one junk byte        -> 403
//	⛔ LEFT OPEN ON PURPOSE  PUT  a post-cutoff bank_date onto a leg whose
//	                            VOUCHER is inside the lock            -> 200
//	                            An April cheque cleared in August is the ordinary
//	                            case; refusing it would delete the feature.

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
)

// bankCutoffFixture is a company with a bank ledger and three payments, all
// dated INSIDE what will become the locked period.
//
// Three, because the interesting legs need three independent states: one leg
// never reconciled, one reconciled INSIDE the period before it was locked, and
// one reconciled AFTER it. The last is the control for the delete.
type bankCutoffFixture struct {
	base     string
	ownerTok string
	bankID   string
	voucher  [3]string
}

func newBankCutoffFixture(t *testing.T, s *Server) bankCutoffFixture {
	t.Helper()
	companyID, ownerTok := aclOwnerCompany(t, s)
	base := "/v1/companies/" + companyID
	sub := "bank-cutoff-" + uuid.NewString()[:8]

	mk := func(name, group string) string {
		t.Helper()
		rec, body := aclRequest(t, s, http.MethodPost, base+"/ledgers", ownerTok,
			map[string]any{"name": name, "group_name": group, "created_by": sub})
		if rec.Code != http.StatusCreated {
			t.Fatalf("create ledger %q = %d, want 201 (%s)", name, rec.Code, rec.Body.String())
		}
		id, _ := body["ledger_id"].(string)
		return id
	}
	bankID := mk("HDFC Current", "Bank Accounts")
	rentID := mk("Rent Paid", "Indirect Expenses")

	f := bankCutoffFixture{base: base, ownerTok: ownerTok, bankID: bankID}
	for i := range f.voucher {
		rec, body := aclRequest(t, s, http.MethodPost, base+"/vouchers/payment", ownerTok, map[string]any{
			// ⛔ BEFORE THE CUTOFF SET BELOW, and that is load-bearing: migration
			// 0045's trigger requires bank_date >= voucher_date, so a bank date
			// INSIDE the locked period is only reachable at all if the voucher is
			// older still. It is also the shape of the door left open on purpose —
			// an old cheque cleared today.
			"voucher_date": "2026-05-10",
			"created_by":   sub,
			"narration":    "cheque 00012" + string(rune('1'+i)),
			"postings": []map[string]any{
				{"ledger_id": rentID, "dr_cr": "D", "amount": "5000.0000"},
				{"ledger_id": bankID, "dr_cr": "C", "amount": "5000.0000"},
			},
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("post payment %d = %d, want 201 (%s)", i, rec.Code, rec.Body.String())
		}
		f.voucher[i], _ = body["voucher_id"].(string)
	}
	return f
}

func (f bankCutoffFixture) leg(i int) string {
	return f.base + "/bank-reconciliation/" + f.bankID + "/" + f.voucher[i] + "/0"
}

// bankDateOnRecord reads the leg's bank date back OUT of the books, because a
// status code is not an oracle in this repo — a 403 that still WROTE is its
// signature failure. "" means the leg is not reconciled.
func (f bankCutoffFixture) bankDateOnRecord(t *testing.T, s *Server, i int) string {
	t.Helper()
	rec, body := aclRequest(t, s, http.MethodGet,
		f.base+"/bank-reconciliation/"+f.bankID+"?as_on=2026-12-31", f.ownerTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET statement = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	for _, l := range jsonList(t, body, "cleared") {
		if id, _ := l["voucher_id"].(string); id == f.voucher[i] {
			d, _ := l["bank_date"].(string)
			return d
		}
	}
	return ""
}

// TestABankDateAssertedIntoAClosedPeriodIsRefused is the driven defect, with
// every door of the class and the control that keeps the feature alive.
func TestABankDateAssertedIntoAClosedPeriodIsRefused(t *testing.T) {
	s := payrollServer(t)
	f := newBankCutoffFixture(t, s)

	// ---- Two reconciliations recorded BEFORE anything is locked. -----------
	// This is the only way a leg can come to be cleared inside a period that is
	// later closed, which is the situation the source-end checks are about.
	if rec, _ := aclRequest(t, s, http.MethodPut, f.leg(1), f.ownerTok,
		map[string]any{"bank_date": "2026-05-20", "created_by": "pre-lock"}); rec.Code != http.StatusOK {
		t.Fatalf("PUT bank_date 2026-05-20 before any cutoff = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if rec, _ := aclRequest(t, s, http.MethodPut, f.leg(2), f.ownerTok,
		map[string]any{"bank_date": "2026-07-14", "created_by": "pre-lock"}); rec.Code != http.StatusOK {
		t.Fatalf("PUT bank_date 2026-07-14 before any cutoff = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}

	levelID := aclOwnerLevelID(t, s, f.base, f.ownerTok)
	const cutoff = "2026-07-01"
	aclSetCutoff(t, s, f.base, levelID, f.ownerTok, cutoff)

	// ---- CONTROL: the cutoff is live on THIS company. ----------------------
	// Without it every 403 below could be any other denial and this test would
	// be worthless.
	if rec, _ := aclRequest(t, s, http.MethodPost, f.base+"/vouchers/journal", f.ownerTok,
		map[string]any{"voucher_date": "2026-05-31", "postings": []any{}}); rec.Code != http.StatusForbidden {
		t.Fatalf("CONTROL: POST /vouchers/journal dated 2026-05-31 under a %s cutoff = %d, want 403 — "+
			"the cutoff is not live here, so nothing below proves anything (%s)",
			cutoff, rec.Code, rec.Body.String())
	}

	// ---- DOOR 1: assert a clearance INTO the closed period. ----------------
	rec, refusal := aclRequest(t, s, http.MethodPut, f.leg(0), f.ownerTok,
		map[string]any{"bank_date": "2026-05-20", "created_by": "attack"})
	if rec.Code != http.StatusForbidden {
		t.Errorf("PUT bank_date=2026-05-20 under a %s cutoff = %d, want 403 — the operator has asserted "+
			"that money moved through the bank inside a period the owner has closed. The cutoff is "+
			"applied from voucherDateFromRequest, which must read bank_date for this route (see "+
			"peekBankDate); reading only voucher_date leaves the PDP with no date at all and cutoffDeny "+
			"skips (%s)", cutoff, rec.Code, rec.Body.String())
	}
	if got := f.bankDateOnRecord(t, s, 0); got != "" {
		t.Errorf("after the refusal the leg reads bank_date %q, want unreconciled — a 403 that still "+
			"WROTE is this repo's signature failure, which is why the books are read back here", got)
	}
	// ⚠️ AND THE REFUSAL MUST NAME THE FIELD THE OPERATOR TYPED. Decision.Reason
	// is written to the client verbatim and this screen has no "voucher date" on
	// it — telling a clerk ticking a cheque that their VOUCHER date is the problem
	// sends them to look at the wrong document. See authz.Request.DateSubject.
	if msg, _ := refusal["error"].(string); msg != "bank date is before the back-dated cutoff of "+cutoff {
		t.Errorf("refusal = %q, want it to name the bank date and the boundary — the operator "+
			"otherwise learns that some period is locked without learning where it starts, or is "+
			"pointed at a field their screen does not have", msg)
	}

	// ---- DOOR 2: MOVE an existing clearance out of the closed period. ------
	// banking.SetBankDate is an upsert (ON CONFLICT ... DO UPDATE), so without the
	// source-end read this re-stamps the leg and walks it out of the lock.
	rec, _ = aclRequest(t, s, http.MethodPut, f.leg(1), f.ownerTok,
		map[string]any{"bank_date": "2026-07-20", "created_by": "attack"})
	if rec.Code != http.StatusForbidden {
		t.Errorf("PUT bank_date=2026-07-20 onto a leg cleared 2026-05-20 under a %s cutoff = %d, want "+
			"403 — the DESTINATION is legal and the SOURCE is inside the lock, so judging the request's "+
			"date alone lets a clearance be lifted out of a closed period. See storedBankDateForLeg (%s)",
			cutoff, rec.Code, rec.Body.String())
	}
	if got := f.bankDateOnRecord(t, s, 1); got != "2026-05-20" {
		t.Errorf("after the refusal the leg reads bank_date %q, want 2026-05-20 unchanged", got)
	}

	// ---- DOOR 3: WITHDRAW a clearance from the closed period. --------------
	rec, refusal = aclRequest(t, s, http.MethodDelete, f.leg(1), f.ownerTok, nil)
	if rec.Code != http.StatusForbidden {
		t.Errorf("DELETE a leg cleared 2026-05-20 under a %s cutoff = %d, want 403 — un-ticking has no "+
			"body and therefore no date of its own; its whole act is on the STORED bank date, which is "+
			"the asymmetry round 3 found on the voucher DELETE, one table across (%s)",
			cutoff, rec.Code, rec.Body.String())
	}
	if got := f.bankDateOnRecord(t, s, 1); got != "2026-05-20" {
		t.Errorf("after the refused DELETE the leg reads bank_date %q, want 2026-05-20 — still cleared", got)
	}
	// The un-tick carries no date of its own, so the refusal has to name the one
	// ON RECORD or the operator cannot tell which of their clearances is locked.
	if msg, _ := refusal["error"].(string); msg !=
		"this bank date of 2026-05-20 is inside the period closed by the back-dated cutoff of "+cutoff {
		t.Errorf("refusal = %q, want it to name the STORED bank date and the boundary", msg)
	}

	// ---- CONTROL: the DELETE is not simply broken. -------------------------
	// Leg 2 cleared on 2026-07-14, after the cutoff. Un-ticking it must work, or
	// the two 403s above would be satisfied by a control that refuses everything.
	if rec, _ := aclRequest(t, s, http.MethodDelete, f.leg(2), f.ownerTok, nil); rec.Code != http.StatusOK {
		t.Errorf("CONTROL: DELETE a leg cleared 2026-07-14 (AFTER the %s cutoff) = %d, want 200 — a "+
			"control that refuses every un-tick would pass every attack leg above and still be broken (%s)",
			cutoff, rec.Code, rec.Body.String())
	}

	// ---- ⛔ THE DOOR LEFT OPEN ON PURPOSE. ---------------------------------
	// The voucher is dated 2026-05-10, inside the lock. Clearing it TODAY, after
	// the cutoff, is the ordinary work of a reconciliation and must still work:
	// the assertion being made is dated 2026-07-14, and it is the assertion the
	// cutoff owns. Judging the voucher's own date here would refuse nearly every
	// tick at any company that has ever locked a period.
	if rec, _ := aclRequest(t, s, http.MethodPut, f.leg(0), f.ownerTok,
		map[string]any{"bank_date": "2026-07-14", "created_by": "ordinary"}); rec.Code != http.StatusOK {
		t.Errorf("PUT bank_date=2026-07-14 on a voucher dated 2026-05-10 under a %s cutoff = %d, want "+
			"200 — an old cheque cleared after the cutoff is the ordinary case and 0045's trigger "+
			"requires bank_date >= voucher_date, so refusing it deletes the feature (%s)",
			cutoff, rec.Code, rec.Body.String())
	}
	if got := f.bankDateOnRecord(t, s, 0); got != "2026-07-14" {
		t.Errorf("the allowed tick reads bank_date %q, want 2026-07-14 — the 200 above must have "+
			"actually written; a status code is not the oracle", got)
	}
}

// TestABankDateSpelledLooselyOrWithTrailingBytesIsStillJudged walks the two doors
// that re-opened this control on OTHER routes four separate times, on the wire.
//
// ⛔ THE LOOSE SPELLING IS A GENUINE HOLE HERE AND WAS NOT ONE ON THE CHALLAN,
// which is why peekBankDate is not a copy of peekDepositDate. masters
// .validateTDSChallan parses deposit_date with exactly "2006-01-02", so a loose
// spelling dies at 400 and never becomes a row. banking.SetBankDate hands the
// string straight to Postgres as `$6::date` — and Postgres accepts `2026-5-20`.
// So a peek that returned nil for what Go cannot parse would let the row land
// INSIDE the locked period, which is round 1's defect exactly.
func TestABankDateSpelledLooselyOrWithTrailingBytesIsStillJudged(t *testing.T) {
	s := payrollServer(t)
	f := newBankCutoffFixture(t, s)

	levelID := aclOwnerLevelID(t, s, f.base, f.ownerTok)
	const cutoff = "2026-07-01"
	aclSetCutoff(t, s, f.base, levelID, f.ownerTok, cutoff)

	// LOOSE SPELLING. `2026-5-20` is not "2006-01-02" and IS a date to Postgres.
	rec, _ := aclRequest(t, s, http.MethodPut, f.leg(0), f.ownerTok,
		map[string]any{"bank_date": "2026-5-20", "created_by": "attack"})
	if rec.Code != http.StatusForbidden {
		t.Errorf("PUT bank_date=2026-5-20 under a %s cutoff = %d, want 403 — Postgres casts this to "+
			"2026-05-20 and stores it INSIDE the locked period, so a peek that answers nil for it turns "+
			"the cutoff off exactly as the loosely-spelled voucher_date did in round 1 (%s)",
			cutoff, rec.Code, rec.Body.String())
	}
	if got := f.bankDateOnRecord(t, s, 0); got != "" {
		t.Errorf("a loosely-spelled bank date was RECORDED as %q inside the locked period", got)
	}

	// TRAILING BYTES. decodeJSON reads the first JSON value and ignores the rest,
	// so the handler accepts this body; a peek using json.Unmarshal would call it
	// "not JSON", answer nil, and switch the cutoff off. Round 5, one route on.
	rec, _ = aclRequestRawKB(t, s, http.MethodPut, f.leg(0), f.ownerTok,
		`{"bank_date":"2026-05-20","created_by":"attack"} trailing`)
	if rec.Code != http.StatusForbidden {
		t.Errorf("PUT a valid bank-date body plus one appended junk byte under a %s cutoff = %d, want "+
			"403 — decodeJSON ignores trailing bytes, so the handler reconciles happily while a peek "+
			"built on json.Unmarshal sees nothing (%s)", cutoff, rec.Code, rec.Body.String())
	}
	if got := f.bankDateOnRecord(t, s, 0); got != "" {
		t.Errorf("a body with trailing bytes was RECORDED as %q inside the locked period", got)
	}

	// CONTROL: an ordinary, well-spelled tick after the cutoff still works.
	if rec, _ := aclRequest(t, s, http.MethodPut, f.leg(0), f.ownerTok,
		map[string]any{"bank_date": "2026-07-14", "created_by": "ordinary"}); rec.Code != http.StatusOK {
		t.Errorf("CONTROL: an ordinary post-cutoff tick = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
}

// TestPeekBankDateIsDecodedTheWayTheHandlerDecodesIt pins the peek itself against
// the four ways this class of peek has drifted from its handler before: the key's
// CASE (encoding/json binds case-insensitively), trailing bytes, a loose spelling,
// and an absent field.
func TestPeekBankDateIsDecodedTheWayTheHandlerDecodesIt(t *testing.T) {
	want := time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)

	t.Run("the date the handler would bind", func(t *testing.T) {
		for _, body := range []string{
			`{"bank_date":"2026-05-20"}`,
			// encoding/json binds tags case-INSENSITIVELY, so setBankDateReq takes
			// all of these and so must the peek. This is round 4's door.
			`{"BANK_DATE":"2026-05-20"}`,
			`{"Bank_Date":"2026-05-20"}`,
			// Decoder.Decode reads the first value and ignores what follows, which
			// is what decodeJSON does. Round 5's door.
			`{"bank_date":"2026-05-20"} trailing`,
			`{"bank_date":"2026-05-20"}{"bank_date":"2026-01-01"}`,
			// A field the request type does not carry is ignored by both.
			`{"bank_date":"2026-05-20","unexpected":1}`,
		} {
			got := peekBankDate([]byte(body))
			if got == nil {
				t.Errorf("peekBankDate(%s) = nil — nil means THE CUTOFF DOES NOT APPLY, while "+
					"handleSetBankDate binds this body's date and records it", body)
				continue
			}
			if !got.Equal(want) {
				t.Errorf("peekBankDate(%s) = %s, want 2026-05-20", body, got.Format("2006-01-02"))
			}
		}
	})

	t.Run("present but unreadable fails CLOSED", func(t *testing.T) {
		// ⛔ THE DIVERGENCE FROM peekDepositDate. Postgres accepts every spelling
		// below through `$6::date` or refuses it at the DB, and the PDP cannot tell
		// which — so it must not answer "no cutoff applies".
		for _, body := range []string{
			`{"bank_date":"2026-5-20"}`,
			`{"bank_date":"20-05-2026"}`,
			`{"bank_date":"May 20, 2026"}`,
			`{"bank_date":"2026-05-20T00:00:00Z"}`,
		} {
			got := peekBankDate([]byte(body))
			if got == nil {
				t.Errorf("peekBankDate(%s) = nil, want the fail-closed zero time — a date the peek "+
					"cannot read but the store CAN is the round-1 defect", body)
				continue
			}
			if !got.IsZero() {
				t.Errorf("peekBankDate(%s) = %s, want the zero time", body, got.Format("2006-01-02"))
			}
		}
	})

	t.Run("absent is somebody else's 400", func(t *testing.T) {
		// handleSetBankDate refuses an empty bank_date with "bank_date is required"
		// before banking.SetBankDate is reached, so nothing can land in a locked
		// period this way and a 400 naming the field is the better answer.
		for _, body := range []string{
			`{}`,
			`{"bank_date":""}`,
			`{"created_by":"x"}`,
			`not json at all`,
		} {
			if got := peekBankDate([]byte(body)); got != nil {
				t.Errorf("peekBankDate(%s) = %s, want nil — the handler answers 400 for this body and "+
					"writes nothing", body, got.Format("2006-01-02"))
			}
		}
	})
}
