package server

// auth_stored_date_cutoff_test.go — THE BACK-DATED CUTOFF MUST ALSO JUDGE THE
// DATE THAT IS ALREADY IN THE BOOKS.
//
// ===========================================================================
// WHY THIS FILE EXISTS, AND WHY IT IS THE THIRD ROUND OF ONE DEFECT
// ===========================================================================
// The cutoff has now failed four times for four different reasons, and every
// previous fix was the fix for the door it was shown:
//
//	auth_payroll_cutoff_test.go       the peek could not FIND the date
//	                                  (wage_month, not voucher_date)
//	auth_tds_challan_cutoff_test.go   same, for deposit_date
//	auth_backdated_spelling_test.go   the peek FOUND it and could not READ it
//	auth_backdated_nested_test.go     it was one level DOWN (sync payload), and
//	                                  in one route not JSON at all (Tally XML)
//
// All four judge THE DATE THE REQUEST CARRIES. A browser re-drive then left a
// locked period without sending a back-dated date at all. Verbatim, on the wire,
// against a company whose Owner level had BackdatedCutoff = 2026-07-01 and a
// journal already posted at 2026-05-01:
//
//	ALTER   PUT .../vouchers/journal/<id>   voucher_date=2026-08-20
//	        -> 200 {"version_no":2,...}
//	DELETE  DELETE .../vouchers/<id>
//	        -> 200 {"op":"DELETE","version_no":3,...}
//	CONTROL POST .../vouchers/journal       voucher_date=2026-05-01
//	        -> 403   (the cutoff was live the whole time)
//
// NEITHER DOOR IS BODY-SHAPED. A DELETE has no body, so drainBody returns nil,
// peekVoucherDate returns nil, and cutoffDeny's nil arm returns early — the
// control never runs. An ALTER has a body, but it states where the voucher is
// GOING, so a post-cutoff date lifts a May voucher clean out of a filed quarter
// while satisfying every check that exists.
//
// WHAT IT COSTS. The cutoff is what makes a filed GST period trustworthy: it is
// set the day the quarter's GSTR-1, GSTR-3B and 26Q go to the portals. An
// operator who could not POST into that quarter could still EMPTY it one voucher
// at a time, or lift each voucher out of it by restating it with next month's
// date. Either way the books stop agreeing with the filed returns, and the
// discrepancy surfaces at the department rather than here. A cutoff you can
// delete around is not a cutoff.
//
// ===========================================================================
// WHAT IS PROVED HERE
// ===========================================================================
// Both ends of the move, in both directions, on all four routes of the class,
// with the CONTROL legs that a blanket refusal would fail — and every refusal
// asserts THE BOOKS, not only the status code, because "a 403 that still wrote"
// is this repository's signature failure.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/khata/cored/internal/authz"
)

const storedCutoff = "2026-07-01"

// ---------------------------------------------------------------------------
// Book-reading helpers. A refusal is only interesting if the books did not move,
// so every assertion below goes through one of these.
// ---------------------------------------------------------------------------

// storedVoucherHead reads a voucher's CURRENT date and version straight back out
// of the books through GET /vouchers/{id} — the same head join
// storedVoucherDateSQL uses. found=false means the voucher is gone (tombstoned
// or never there), which is what a successful DELETE looks like.
func storedVoucherHead(t *testing.T, s *Server, base, voucherID, tok string) (date string, versionNo int, found bool) {
	t.Helper()
	rec, body := aclRequest(t, s, http.MethodGet, base+"/vouchers/"+voucherID, tok, nil)
	if rec.Code == http.StatusNotFound {
		return "", 0, false
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /vouchers/%s = %d, want 200 or 404 (%s)", voucherID, rec.Code, rec.Body.String())
	}
	d, _ := body["voucher_date"].(string)
	v, _ := body["version_no"].(float64)
	return d, int(v), true
}

// dayBookHas reports whether the Day Book — the report an auditor actually reads
// — still shows this voucher over the window. It is the second, independent
// witness: GET /vouchers/{id} could in principle answer from a head row while a
// tombstone had already reversed the voucher out of every balance, and the
// converse. A delete that was REFUSED must leave the voucher in BOTH.
func dayBookHas(t *testing.T, s *Server, base, voucherNo, tok string) bool {
	t.Helper()
	rec, body := aclRequest(t, s, http.MethodGet,
		base+"/day-book?from=2020-01-01&to=2030-12-31", tok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /day-book = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	entries, _ := body["entries"].([]any)
	for _, e := range entries {
		m, _ := e.(map[string]any)
		if no, _ := m["voucher_no"].(string); no == voucherNo {
			return true
		}
	}
	return false
}

// postJournal posts one balanced journal and returns its voucher id. It runs
// BEFORE the cutoff is set, which is the only honest way to get a voucher inside
// what later becomes a locked period — exactly how a real filed quarter comes to
// contain vouchers.
func postJournal(t *testing.T, s *Server, f spellingFixture, date, voucherNo string) string {
	t.Helper()
	rec, body := aclRequest(t, s, http.MethodPost, f.base+"/vouchers/journal", f.ownerTok,
		f.journal(date, voucherNo))
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed POST /vouchers/journal %s = %d, want 201 (%s)",
			date, rec.Code, rec.Body.String())
	}
	id, _ := body["voucher_id"].(string)
	if id == "" {
		t.Fatalf("seed POST /vouchers/journal %s returned no voucher_id (%s)", date, rec.Body.String())
	}
	return id
}

// ===========================================================================
// DOOR 1 + DOOR 2 — the two that were DRIVEN
// ===========================================================================

// TestCutoffRefusesAnAlterThatLiftsAVoucherOutOfALockedPeriod is the ALTER door,
// in the direction the browser drove it: the request's OWN date is legal, and the
// voucher it names is not.
//
//	CONTROL POST /vouchers/journal  2026-05-01           -> 403 (cutoff is live)
//	ATTACK  PUT  /vouchers/journal/<May voucher>  → 2026-08-20
//	                                                    -> MUST be 403,
//	                                                       and the May voucher
//	                                                       must still be dated
//	                                                       2026-05-01 at v1.
func TestCutoffRefusesAnAlterThatLiftsAVoucherOutOfALockedPeriod(t *testing.T) {
	s := payrollServer(t)
	f := newSpellingFixture(t, s)

	// Seed the voucher BEFORE the lock exists — a filed quarter's vouchers were
	// all posted while the quarter was open.
	vid := postJournal(t, s, f, "2026-05-01", "STORED-ALTER-OUT")

	levelID := aclOwnerLevelID(t, s, f.base, f.ownerTok)
	aclSetCutoff(t, s, f.base, levelID, f.ownerTok, storedCutoff)

	// ------------------------------------------------------------------ CONTROL
	// Without this leg every 403 below could be any other denial and this test
	// would be worth nothing.
	rec, _ := aclRequest(t, s, http.MethodPost, f.base+"/vouchers/journal", f.ownerTok,
		f.journal("2026-05-01", "STORED-ALTER-CONTROL"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("CONTROL: POST /vouchers/journal voucher_date=2026-05-01 under a %s cutoff = %d, "+
			"want 403 — the cutoff is not live on this company (%s)", storedCutoff, rec.Code, rec.Body.String())
	}

	// ------------------------------------------------------------------- ATTACK
	rec, _ = aclRequest(t, s, http.MethodPut, f.base+"/vouchers/journal/"+vid, f.ownerTok,
		f.journal("2026-08-20", "STORED-ALTER-OUT"))
	date, version, found := storedVoucherHead(t, s, f.base, vid, f.ownerTok)
	if rec.Code != http.StatusForbidden {
		t.Errorf("ATTACK: PUT /vouchers/journal/<a voucher dated 2026-05-01> with "+
			"voucher_date=2026-08-20 under a %s cutoff = %d, want 403 — the voucher was LIFTED OUT of a "+
			"filed period by restating it with a legal date. The PDP only ever judged the date the "+
			"REQUEST carried; the date that decides this act is the one already in the database. "+
			"The books now say the voucher is dated %s at version %d (%s)",
			storedCutoff, rec.Code, date, version, rec.Body.String())
	}
	// ⛔ THE BOOKS, NOT THE STATUS CODE. A 403 that still wrote is this repo's
	// signature failure.
	if !found {
		t.Fatalf("the voucher is GONE from the books after a refused ALTER")
	}
	if date != "2026-05-01" {
		t.Errorf("after the refused ALTER the voucher is dated %s, want 2026-05-01 — the refusal did "+
			"not prevent the move, so the voucher left the locked period anyway", date)
	}
	if version != 1 {
		t.Errorf("after the refused ALTER the voucher is at version %d, want 1 — a new version was "+
			"appended, so the write happened behind the 403", version)
	}
}

// TestCutoffRefusesAnAlterThatPushesAVoucherIntoALockedPeriod is the OPPOSITE
// direction, and it is here because a fix that judged ONLY the stored date would
// pass the test above and re-open this. Both ENDS of the move must be outside the
// lock: the source, so a voucher cannot be lifted out, and the destination, so
// one cannot be pushed in.
func TestCutoffRefusesAnAlterThatPushesAVoucherIntoALockedPeriod(t *testing.T) {
	s := payrollServer(t)
	f := newSpellingFixture(t, s)

	// This one starts AFTER the cutoff, so the stored end is legal.
	vid := postJournal(t, s, f, "2026-09-10", "STORED-ALTER-IN")

	levelID := aclOwnerLevelID(t, s, f.base, f.ownerTok)
	aclSetCutoff(t, s, f.base, levelID, f.ownerTok, storedCutoff)

	rec, _ := aclRequest(t, s, http.MethodPut, f.base+"/vouchers/journal/"+vid, f.ownerTok,
		f.journal("2026-05-01", "STORED-ALTER-IN"))
	date, version, found := storedVoucherHead(t, s, f.base, vid, f.ownerTok)
	if rec.Code != http.StatusForbidden {
		t.Errorf("PUT /vouchers/journal/<a voucher dated 2026-09-10> with voucher_date=2026-05-01 "+
			"under a %s cutoff = %d, want 403 — a voucher was pushed INTO a filed period. Judging only "+
			"the STORED date closes the lift-out door and re-opens this one; both ends must clear the "+
			"cutoff. The books now say %s at version %d (%s)",
			storedCutoff, rec.Code, date, version, rec.Body.String())
	}
	if !found {
		t.Fatalf("the voucher is GONE from the books after a refused ALTER")
	}
	if date != "2026-09-10" || version != 1 {
		t.Errorf("after the refused ALTER the voucher is dated %s at version %d, want 2026-09-10 at "+
			"version 1 — the write happened behind the 403", date, version)
	}
}

// TestCutoffRefusesADeleteOutOfALockedPeriod is the DELETE door. It is the purest
// case in the class: THERE IS NO BODY AT ALL, so the only date in the whole
// request is the stored one, and every peek written in rounds 1 and 2 returns nil
// for it.
func TestCutoffRefusesADeleteOutOfALockedPeriod(t *testing.T) {
	s := payrollServer(t)
	f := newSpellingFixture(t, s)

	const no = "STORED-DELETE-IN-LOCK"
	vid := postJournal(t, s, f, "2026-05-01", no)

	levelID := aclOwnerLevelID(t, s, f.base, f.ownerTok)
	aclSetCutoff(t, s, f.base, levelID, f.ownerTok, storedCutoff)

	// ------------------------------------------------------------------ CONTROL
	rec, _ := aclRequest(t, s, http.MethodPost, f.base+"/vouchers/journal", f.ownerTok,
		f.journal("2026-05-01", "STORED-DELETE-CONTROL"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("CONTROL: POST /vouchers/journal voucher_date=2026-05-01 under a %s cutoff = %d, "+
			"want 403 (%s)", storedCutoff, rec.Code, rec.Body.String())
	}

	// ------------------------------------------------------------------- ATTACK
	rec, _ = aclRequest(t, s, http.MethodDelete, f.base+"/vouchers/"+vid, f.ownerTok, nil)
	_, version, found := storedVoucherHead(t, s, f.base, vid, f.ownerTok)
	inBooks := dayBookHas(t, s, f.base, no, f.ownerTok)
	if rec.Code != http.StatusForbidden {
		t.Errorf("ATTACK: DELETE /vouchers/<a voucher dated 2026-05-01> under a %s cutoff = %d, want "+
			"403 — a voucher was removed from a FILED period. The request has no body, so every peek "+
			"in the cutoff path returns nil and cutoffDeny returns early on a nil date: the control "+
			"never ran. The voucher head is now %s and the Day Book shows it: %v (%s)",
			storedCutoff, rec.Code, map[bool]string{true: "STILL PRESENT",
				false: "GONE FROM THE BOOKS"}[found], inBooks, rec.Body.String())
	}
	// ⛔ ASSERT THE BOOKS TWICE, through two independent reads.
	if !found {
		t.Errorf("after a refused DELETE the voucher is GONE from GET /vouchers/%s — the tombstone was "+
			"written behind the 403", vid)
	}
	if version != 1 {
		t.Errorf("after a refused DELETE the voucher is at version %d, want 1 — a DELETE version was "+
			"appended behind the 403", version)
	}
	if !inBooks {
		t.Errorf("after a refused DELETE the Day Book no longer shows voucher %s — it has been "+
			"reversed out of every balance and every report over the filed period", no)
	}
}

// TestCutoffStillAllowsAlterAndDeleteWhollyOutsideTheLock IS THE HALF THAT IS
// USUALLY MISSING, and without it every assertion above is satisfied by a fix
// that refuses all editing forever.
//
// Four legs, all of which must SUCCEED under a live cutoff:
//
//	ALTER  a post-cutoff voucher to another post-cutoff date  -> 200
//	DELETE a post-cutoff voucher                              -> 200, and it is
//	                                                             gone from the
//	                                                             books (the
//	                                                             refusal legs
//	                                                             above prove
//	                                                             nothing unless a
//	                                                             DELETE can still
//	                                                             delete)
//	CREATE a post-cutoff voucher                              -> 201
//	ALTER  an UNDATED voucher                                 -> 200 (a NULL
//	                                                             voucher_date is
//	                                                             dated into no
//	                                                             period, so it
//	                                                             must not be
//	                                                             mistaken for the
//	                                                             fail-closed
//	                                                             sentinel)
func TestCutoffStillAllowsAlterAndDeleteWhollyOutsideTheLock(t *testing.T) {
	s := payrollServer(t)
	f := newSpellingFixture(t, s)

	alterable := postJournal(t, s, f, "2026-08-05", "STORED-OK-ALTER")
	deletable := postJournal(t, s, f, "2026-08-06", "STORED-OK-DELETE")
	// An UNDATED voucher: ledger.Post stores an empty date as SQL NULL. It needs
	// an explicit voucher_no, because the auto-numbering branch parses the date.
	undated := postJournal(t, s, f, "", "STORED-OK-UNDATED")

	levelID := aclOwnerLevelID(t, s, f.base, f.ownerTok)
	aclSetCutoff(t, s, f.base, levelID, f.ownerTok, storedCutoff)

	// ALTER, both ends after the cutoff.
	rec, _ := aclRequest(t, s, http.MethodPut, f.base+"/vouchers/journal/"+alterable, f.ownerTok,
		f.journal("2026-08-25", "STORED-OK-ALTER"))
	if rec.Code != http.StatusOK {
		t.Errorf("ALLOWED: PUT /vouchers/journal/<a voucher dated 2026-08-05> with "+
			"voucher_date=2026-08-25 under a %s cutoff = %d, want 200 — BOTH ends are after the cutoff, "+
			"so this is an ordinary correction. A fix that refuses it stops the business trading (%s)",
			storedCutoff, rec.Code, rec.Body.String())
	} else if date, version, _ := storedVoucherHead(t, s, f.base, alterable, f.ownerTok); date != "2026-08-25" || version != 2 {
		t.Errorf("the ALLOWED alter returned 200 but the books say %s at version %d, want 2026-08-25 "+
			"at version 2 — a 200 that did not write is the mirror of the defect this file is about",
			date, version)
	}

	// DELETE, wholly after the cutoff.
	rec, _ = aclRequest(t, s, http.MethodDelete, f.base+"/vouchers/"+deletable, f.ownerTok, nil)
	if rec.Code != http.StatusOK {
		t.Errorf("ALLOWED: DELETE /vouchers/<a voucher dated 2026-08-06> under a %s cutoff = %d, want "+
			"200 — deleting a voucher in an OPEN period is an ordinary correction (%s)",
			storedCutoff, rec.Code, rec.Body.String())
	} else if _, _, found := storedVoucherHead(t, s, f.base, deletable, f.ownerTok); found {
		t.Errorf("the ALLOWED delete returned 200 but the voucher is still readable — the delete did " +
			"not happen, so the refusal legs above prove nothing")
	}

	// CREATE after the cutoff (round 1's own control leg, re-asserted because the
	// store read now runs on this company).
	rec, _ = aclRequest(t, s, http.MethodPost, f.base+"/vouchers/journal", f.ownerTok,
		f.journal("2026-08-15", "STORED-OK-CREATE"))
	if rec.Code != http.StatusCreated {
		t.Errorf("ALLOWED: POST /vouchers/journal voucher_date=2026-08-15 under a %s cutoff = %d, want "+
			"201 — a create names no stored voucher, so the new read must not touch it (%s)",
			storedCutoff, rec.Code, rec.Body.String())
	}

	// ALTER of an UNDATED voucher. A NULL stored date is "dated into no period",
	// not "unreadable": treating it as the fail-closed sentinel would refuse
	// every edit of every undated voucher at any company with a cutoff.
	rec, _ = aclRequest(t, s, http.MethodPut, f.base+"/vouchers/journal/"+undated, f.ownerTok,
		f.journal("2026-08-26", "STORED-OK-UNDATED"))
	if rec.Code != http.StatusOK {
		t.Errorf("ALLOWED: PUT /vouchers/journal/<an UNDATED voucher> with voucher_date=2026-08-26 "+
			"under a %s cutoff = %d, want 200 — ledger.Post stores an absent date as SQL NULL, which is "+
			"dated into NO period and cannot be inside the lock. Reading NULL as the zero-time sentinel "+
			"would refuse every edit of every undated voucher (%s)",
			storedCutoff, rec.Code, rec.Body.String())
	}
}

// TestCutoffFailsClosedOnAVoucherItCannotRead pins the fail-closed direction the
// pay run established: a name the PDP cannot resolve to a period must be
// REFUSED, not waved through.
//
// Two shapes:
//
//	a voucher id that does not exist       -> 403 (not 404). "I could not find
//	                                         out which period this is in" must
//	                                         never be spelled the same way as
//	                                         "it is not in a locked one".
//	a voucher already tombstoned          -> 403 for a second DELETE, because the
//	                                         head join excludes tombstones and so
//	                                         resolves no period.
//
// ⛔ AND THE COUNTERWEIGHT: with the cutoff CLEARED, both are the handler's own
// 404. The fail-closed sentinel is a CUTOFF signal, not a global refusal — if it
// bit where no period is locked it would break every ordinary 404.
func TestCutoffFailsClosedOnAVoucherItCannotRead(t *testing.T) {
	s := payrollServer(t)
	f := newSpellingFixture(t, s)

	ghost := uuid.NewString()
	tombstoned := postJournal(t, s, f, "2026-08-07", "STORED-TOMB")

	levelID := aclOwnerLevelID(t, s, f.base, f.ownerTok)

	// With NO cutoff set, both are ordinary 404s / 200s and nothing here applies.
	rec, _ := aclRequest(t, s, http.MethodDelete, f.base+"/vouchers/"+ghost, f.ownerTok, nil)
	if rec.Code == http.StatusForbidden {
		t.Fatalf("with NO cutoff set, DELETE /vouchers/<unknown id> = 403 — the fail-closed sentinel "+
			"must be a CUTOFF signal, not a blanket refusal, or every ordinary 404 on this route "+
			"becomes an authorization error (%s)", rec.Body.String())
	}
	rec, _ = aclRequest(t, s, http.MethodDelete, f.base+"/vouchers/"+tombstoned, f.ownerTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("with NO cutoff set, DELETE /vouchers/<a live voucher> = %d, want 200 (%s)",
			rec.Code, rec.Body.String())
	}

	aclSetCutoff(t, s, f.base, levelID, f.ownerTok, storedCutoff)

	rec, body := aclRequest(t, s, http.MethodDelete, f.base+"/vouchers/"+ghost, f.ownerTok, nil)
	if rec.Code != http.StatusForbidden {
		t.Errorf("DELETE /vouchers/<unknown id> under a %s cutoff = %d, want 403 — a voucher whose "+
			"period cannot be established must be refused. Returning the handler's 404 here would mean "+
			"a store error or an RLS miss became an ALLOW, which is exactly how the DELETE door was "+
			"open in the first place (%s)", storedCutoff, rec.Code, rec.Body.String())
	}
	// The refusal must say WHICH of the two things happened, and must not claim
	// the operator sent a back-dated date — they sent no date at all.
	if msg, _ := body["error"].(string); !strings.Contains(msg, storedCutoff) ||
		!strings.Contains(msg, "could not be read") {
		t.Errorf("the refusal reason %q neither names the cutoff %s nor says the stored date could "+
			"not be read — an operator told only \"forbidden\" cannot tell a period lock apart from a "+
			"bad id", msg, storedCutoff)
	}

	// A SECOND delete of an already-tombstoned voucher: no live head, so no
	// period, so refused.
	rec, _ = aclRequest(t, s, http.MethodDelete, f.base+"/vouchers/"+tombstoned, f.ownerTok, nil)
	if rec.Code != http.StatusForbidden {
		t.Errorf("a SECOND DELETE of an already-tombstoned voucher under a %s cutoff = %d, want 403 — "+
			"the head join excludes tombstones, so no period could be established (%s)",
			storedCutoff, rec.Code, rec.Body.String())
	}
}

// ===========================================================================
// DOOR 3 — /sync/commit, THE SAME TWO ACTS FROM AN OFFLINE NODE
// ===========================================================================

// syncIntent builds one /sync/commit intent body.
func syncIntent(op, voucherID string, payload map[string]any) map[string]any {
	return map[string]any{"intents": []any{map[string]any{
		"idempotency_key":   "stored-" + op + "-" + voucherID[:8],
		"client_voucher_id": voucherID,
		"op":                op,
		"class":             "Journal",
		"node_id":           "stored-node",
		"base_version":      1,
		"hlc":               map[string]any{"wall_millis": 1, "counter": 1, "node_id": "stored-node"},
		"payload":           payload,
	}}}
}

// TestCutoffRefusesASyncAlterOrDeleteOutOfALockedPeriod closes the door round 2
// half-closed. Round 2 taught the peek to walk nested bodies, which covered a
// CREATE intent's payload.voucher_date. An ALTER or DELETE intent is the round-3
// shape: sync.Service.applyOne routes OpAlter to ledger.AlterVoucher and OpDelete
// to ledger.DeleteVoucher against the intent's client_voucher_id, so the SAME two
// acts are reachable through a route nobody thinks of as an editing route.
//
// ⛔ THIS IS NOT A DOOR ANYBODY DROVE. It is here because rounds 1 and 2 each
// fixed what they were shown and each was followed by another door; the
// enumeration is the point of this round.
func TestCutoffRefusesASyncAlterOrDeleteOutOfALockedPeriod(t *testing.T) {
	s := payrollServer(t)
	f := newSpellingFixture(t, s)

	const alterNo = "STORED-SYNC-ALTER"
	const deleteNo = "STORED-SYNC-DELETE"
	alterID := postJournal(t, s, f, "2026-05-02", alterNo)
	deleteID := postJournal(t, s, f, "2026-05-03", deleteNo)

	levelID := aclOwnerLevelID(t, s, f.base, f.ownerTok)
	aclSetCutoff(t, s, f.base, levelID, f.ownerTok, storedCutoff)

	// ALTER intent, moving a May voucher to August.
	rec, _ := aclRequest(t, s, http.MethodPost, f.base+"/sync/commit", f.ownerTok,
		syncIntent("ALTER", alterID, f.journal("2026-08-20", alterNo)))
	date, version, _ := storedVoucherHead(t, s, f.base, alterID, f.ownerTok)
	if rec.Code != http.StatusForbidden {
		t.Errorf("POST /sync/commit op=ALTER on a voucher dated 2026-05-02, payload date 2026-08-20, "+
			"under a %s cutoff = %d, want 403 — the same lift-out the PUT performs, through the offline "+
			"seam. The books now say %s at version %d (%s)",
			storedCutoff, rec.Code, date, version, rec.Body.String())
	}
	if date != "2026-05-02" || version != 1 {
		t.Errorf("after the refused sync ALTER the books say %s at version %d, want 2026-05-02 at "+
			"version 1 — the commit happened behind the 403", date, version)
	}

	// DELETE intent. sync's DeleteVoucher takes only the id; the payload is
	// irrelevant, which is precisely why no body peek can see this act.
	rec, _ = aclRequest(t, s, http.MethodPost, f.base+"/sync/commit", f.ownerTok,
		syncIntent("DELETE", deleteID, f.journal("2026-08-21", deleteNo)))
	_, version, found := storedVoucherHead(t, s, f.base, deleteID, f.ownerTok)
	if rec.Code != http.StatusForbidden {
		t.Errorf("POST /sync/commit op=DELETE on a voucher dated 2026-05-03 under a %s cutoff = %d, "+
			"want 403 — a filed period was emptied through the offline seam (%s)",
			storedCutoff, rec.Code, rec.Body.String())
	}
	if !found || version != 1 {
		t.Errorf("after the refused sync DELETE the voucher is found=%v at version %d, want found=true "+
			"at version 1 — the tombstone was committed behind the 403", found, version)
	}
	if !dayBookHas(t, s, f.base, deleteNo, f.ownerTok) {
		t.Errorf("after the refused sync DELETE the Day Book no longer shows %s", deleteNo)
	}

	// ⛔ CONTROL: a CREATE intent dated after the cutoff must still commit. Its
	// client_voucher_id is a NEW id that does not exist yet, so a fix that failed
	// closed on every unresolvable name would break every offline commit.
	newID := uuid.NewString()
	rec, body := aclRequest(t, s, http.MethodPost, f.base+"/sync/commit", f.ownerTok,
		syncIntent("CREATE", newID, f.journal("2026-08-22", "")))
	if rec.Code != http.StatusOK {
		t.Fatalf("CONTROL: POST /sync/commit op=CREATE voucher_date=2026-08-22 under a %s cutoff = %d, "+
			"want 200 — a CREATE intent names a voucher that does NOT exist yet (the node minted the id "+
			"offline), so it must not be looked up and must not fail closed (%s)",
			storedCutoff, rec.Code, rec.Body.String())
	}
	outcomes, _ := body["outcomes"].([]any)
	if len(outcomes) != 1 {
		t.Fatalf("CONTROL sync CREATE returned %d outcomes, want 1 (%s)", len(outcomes), rec.Body.String())
	}
	if o, _ := outcomes[0].(map[string]any); o["status"] != "COMMITTED" {
		t.Errorf("CONTROL sync CREATE outcome status = %v, want COMMITTED — the 200 did not commit (%s)",
			o["status"], rec.Body.String())
	}
}

// TestASyncBatchIsOnlyAsPermittedAsItsMostRefusedIntent pins the batch rule the
// walk in walkVoucherDates states for CREATE intents, applied to the STORED end:
// one legal intent must not carry an illegal one in with it.
//
// ⛔ AND IT PINS THE DEDUPE. sync.Node emits create-then-alter-then-delete against
// ONE target (its own node_test.go asserts exactly that outbox), so a real batch
// names the same voucher twice. storedVoucherDateForRequest refuses when the rows
// it resolved do not number the ids it asked about — the fail-closed door for a
// voucher that does not exist — so without namedVouchers.deduped() an ORDINARY
// offline batch resolves one row for two ids and is refused. Both halves are
// asserted here because they pull in opposite directions.
func TestASyncBatchIsOnlyAsPermittedAsItsMostRefusedIntent(t *testing.T) {
	s := payrollServer(t)
	f := newSpellingFixture(t, s)

	const inLockNo = "STORED-BATCH-MAY"
	const openNo = "STORED-BATCH-AUG"
	inLock := postJournal(t, s, f, "2026-05-05", inLockNo)
	open := postJournal(t, s, f, "2026-08-05", openNo)

	levelID := aclOwnerLevelID(t, s, f.base, f.ownerTok)
	aclSetCutoff(t, s, f.base, levelID, f.ownerTok, storedCutoff)

	intent := func(key, op, vid string, payload map[string]any) map[string]any {
		return map[string]any{
			"idempotency_key": key, "client_voucher_id": vid, "op": op, "class": "Journal",
			"node_id": "batch-node", "base_version": 1,
			"hlc":     map[string]any{"wall_millis": 1, "counter": 1, "node_id": "batch-node"},
			"payload": payload,
		}
	}

	// ONE legal ALTER (August -> August) plus ONE illegal DELETE (out of May).
	rec, _ := aclRequest(t, s, http.MethodPost, f.base+"/sync/commit", f.ownerTok,
		map[string]any{"intents": []any{
			intent("batch-ok", "ALTER", open, f.journal("2026-08-25", openNo)),
			intent("batch-bad", "DELETE", inLock, f.journal("2026-08-26", inLockNo)),
		}})
	if rec.Code != http.StatusForbidden {
		t.Errorf("POST /sync/commit with a legal ALTER and a DELETE out of a locked period = %d, want "+
			"403 — a batch is only as permitted as its most-refused member; sync.Commit commits its "+
			"intents together, so \"the allowed one went in\" would be indistinguishable to the operator "+
			"from \"both did\" (%s)", rec.Code, rec.Body.String())
	}
	if _, version, found := storedVoucherHead(t, s, f.base, inLock, f.ownerTok); !found || version != 1 {
		t.Errorf("the in-lock voucher is found=%v at version %d after the refused batch, want "+
			"found=true at version 1", found, version)
	}
	if date, version, _ := storedVoucherHead(t, s, f.base, open, f.ownerTok); date != "2026-08-05" || version != 1 {
		t.Errorf("the OPEN voucher is %s at version %d after the refused batch, want 2026-08-05 at "+
			"version 1 — the permitted intent committed anyway, so the batch was partially applied",
			date, version)
	}

	// ⛔ CONTROL: the ordinary offline shape — TWO intents naming the SAME
	// post-cutoff voucher — must still commit. This is what the dedupe protects.
	rec, _ = aclRequest(t, s, http.MethodPost, f.base+"/sync/commit", f.ownerTok,
		map[string]any{"intents": []any{
			intent("batch-dup-1", "ALTER", open, f.journal("2026-08-27", openNo)),
			intent("batch-dup-2", "ALTER", open, f.journal("2026-08-28", openNo)),
		}})
	if rec.Code != http.StatusOK {
		t.Errorf("CONTROL: POST /sync/commit with TWO intents naming the SAME post-cutoff voucher = %d, "+
			"want 200 — sync.Node emits several intents per target, and the fail-closed count check "+
			"refuses a batch whose repeated names resolve to fewer rows than ids. namedVouchers.deduped() "+
			"is what keeps that from breaking every ordinary offline commit (%s)",
			rec.Code, rec.Body.String())
	}
}

// ===========================================================================
// DOOR 4 — /import/tally-xml, THE SAME TWO ACTS BY TALLY GUID
// ===========================================================================

// tallyEnvelope wraps voucher fragments in the envelope
// integration.ParseTallyEnvelope reads (the same shape migration's own
// reconcilefull_test.go uses).
func tallyEnvelope(vouchers ...string) string {
	return `<ENVELOPE>
  <HEADER><TALLYREQUEST>Import</TALLYREQUEST></HEADER>
  <BODY><DESC><STATICVARIABLES><SVCURRENTCOMPANY>Stored Date Co</SVCURRENTCOMPANY></STATICVARIABLES></DESC>
    <DATA>` + strings.Join(vouchers, "") + `</DATA>
  </BODY>
</ENVELOPE>`
}

// tallyVoucher is one <VOUCHER> message with a GUID, an ACTION and two balanced
// ledger lines against the fixture's ledgers. VOUCHERNUMBER is supplied for the
// reason spellingFixture.journal supplies voucher_no: it keeps ledger.Post off
// the auto-numbering branch, so nothing but the PDP stands between the request
// and the books.
func tallyVoucher(action, guid, date, voucherNo string) string {
	return `
      <TALLYMESSAGE>
        <VOUCHER VCHTYPE="Journal" ACTION="` + action + `">
          <GUID>` + guid + `</GUID>
          <DATE>` + date + `</DATE>
          <VOUCHERNUMBER>` + voucherNo + `</VOUCHERNUMBER>
          <NARRATION>stored-date probe</NARRATION>
          <LEDGERENTRIES.LIST>
            <LEDGERNAME>Spelling Sundry Expenses</LEDGERNAME>
            <ISDEEMEDPOSITIVE>No</ISDEEMEDPOSITIVE>
            <AMOUNT>1000.00</AMOUNT>
          </LEDGERENTRIES.LIST>
          <LEDGERENTRIES.LIST>
            <LEDGERNAME>Spelling Bank A/c</LEDGERNAME>
            <ISDEEMEDPOSITIVE>Yes</ISDEEMEDPOSITIVE>
            <AMOUNT>-1000.00</AMOUNT>
          </LEDGERENTRIES.LIST>
        </VOUCHER>
      </TALLYMESSAGE>`
}

// importXML posts a raw Tally envelope through the real router.
func importXML(t *testing.T, s *Server, base, tok, xml string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, base+"/import/tally-xml", strings.NewReader(xml))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/xml")
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	return rec
}

// TestCutoffRefusesATallyXMLAlterOrDeleteOutOfALockedPeriod is the fourth door.
// migration.Importer dispatches on the Tally ACTION attribute: ACTION=Delete
// resolves the GUID with VoucherIDBySourceGUID and calls ledger.DeleteVoucher,
// and ACTION=Alter calls AlterVoucher. Round 2 taught the PDP to read the
// envelope's <DATE> elements, which judges only where a voucher is GOING — so an
// ACTION=Delete message carrying a perfectly legal <DATE> tombstoned a voucher
// inside a filed quarter, and one import can carry thousands of them.
//
// ⛔ ALSO NOT A DOOR ANYBODY DROVE.
func TestCutoffRefusesATallyXMLAlterOrDeleteOutOfALockedPeriod(t *testing.T) {
	s := payrollServer(t)
	f := newSpellingFixture(t, s)

	// Import the voucher first, while the period is open, so it carries a
	// source_guid the later Delete message can name.
	const guid = "stored-guid-0001"
	rec := importXML(t, s, f.base, f.ownerTok, tallyEnvelope(tallyVoucher("Create", guid, "20260504", "XML-SEED")))
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("seed import (ACTION=Create, 2026-05-04) = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}

	levelID := aclOwnerLevelID(t, s, f.base, f.ownerTok)
	aclSetCutoff(t, s, f.base, levelID, f.ownerTok, storedCutoff)

	countMay := func() int {
		t.Helper()
		_, body := aclRequest(t, s, http.MethodGet,
			f.base+"/day-book?from=2026-05-01&to=2026-05-31", f.ownerTok, nil)
		entries, _ := body["entries"].([]any)
		return len(entries)
	}
	before := countMay()
	if before == 0 {
		t.Fatalf("the seeded May voucher is not in the Day Book, so nothing below proves anything")
	}

	// ATTACK: delete it, with a post-cutoff <DATE> so every date the request
	// carries is legal.
	rec = importXML(t, s, f.base, f.ownerTok,
		tallyEnvelope(tallyVoucher("Delete", guid, "20260820", "XML-SEED")))
	if rec.Code != http.StatusForbidden {
		t.Errorf("POST /import/tally-xml ACTION=Delete on a voucher dated 2026-05-04 (envelope <DATE> "+
			"20260820, i.e. legal) under a %s cutoff = %d, want 403 — the importer resolves the GUID and "+
			"calls ledger.DeleteVoucher, so a filed period is emptied by an IMPORT. Round 2's peek reads "+
			"the envelope's <DATE>, which says only where a voucher is going (%s)",
			storedCutoff, rec.Code, rec.Body.String())
	}
	if after := countMay(); after != before {
		t.Errorf("the May Day Book had %d entries before the refused import and %d after — the "+
			"tombstone was written behind the 403", before, after)
	}

	// ATTACK 2: alter it forward, out of the lock.
	rec = importXML(t, s, f.base, f.ownerTok,
		tallyEnvelope(tallyVoucher("Alter", guid, "20260821", "XML-SEED")))
	if rec.Code != http.StatusForbidden {
		t.Errorf("POST /import/tally-xml ACTION=Alter moving a voucher dated 2026-05-04 to 2026-08-21 "+
			"under a %s cutoff = %d, want 403 (%s)", storedCutoff, rec.Code, rec.Body.String())
	}
	if after := countMay(); after != before {
		t.Errorf("the May Day Book had %d entries before the refused ALTER import and %d after", before, after)
	}

	// ⛔ CONTROL: an ordinary post-cutoff CREATE import must still work, and its
	// GUID is UNKNOWN to this company. Failing closed on an unresolvable GUID
	// would refuse every first-time import at any company with a cutoff.
	rec = importXML(t, s, f.base, f.ownerTok,
		tallyEnvelope(tallyVoucher("Create", "stored-guid-0002", "20260823", "XML-OK-1")))
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Errorf("CONTROL: POST /import/tally-xml ACTION=Create <DATE>20260823 under a %s cutoff = %d, "+
			"want 200 — an unknown GUID names no stored voucher and must not fail closed (%s)",
			storedCutoff, rec.Code, rec.Body.String())
	}
	// ⛔ CONTROL 2: ACTION=Alter with an UNKNOWN guid is treated by the importer
	// as a CREATE, dated by its own <DATE>. A post-cutoff one must post.
	rec = importXML(t, s, f.base, f.ownerTok,
		tallyEnvelope(tallyVoucher("Alter", "stored-guid-0003", "20260824", "XML-OK-2")))
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Errorf("CONTROL: POST /import/tally-xml ACTION=Alter with an UNKNOWN guid, <DATE>20260824, "+
			"under a %s cutoff = %d, want 200 — migration.Importer treats it as a CREATE, so refusing it "+
			"breaks every Alter-flavoured first import (%s)", storedCutoff, rec.Code, rec.Body.String())
	}
}

// ===========================================================================
// THE ROUTE LEDGER — the closure gate, so there is no round 4
// ===========================================================================

// writeRoutesNamingADocumentButNotMovingIt is the ALLOWLIST half of the closure
// gate below: write routes that DO carry an existing document's id in the path
// yet cannot take a ledger entry into or out of a locked period. Each entry
// records WHY, because the whole reason this is round 3 is that "it looked
// harmless" was decided by silence three times.
var writeRoutesNamingADocumentButNotMovingIt = map[string]string{
	// Pins an IRN/ARN onto a voucher. ledger.RegisterExternalReference writes ONE
	// row in voucher_external_reference and NO voucher_version: no date, no
	// amount and no posting changes. Its only effect on the voucher is to FREEZE
	// it, i.e. to hard-block later alters and deletes — it makes the filed period
	// MORE immutable, never less, so it cannot be a way around the lock.
	"POST /v1/companies/{companyID}/vouchers/{voucherID}/external-reference": "" +
		"writes no voucher_version; freezing only tightens",

	// Ticking a cheque off against a statement. banking.SetBankDate writes only
	// bank_reconciliation rows (invariant I-2: reconciling moves nothing), so no
	// ledger figure in the filed period changes and no filed return is affected —
	// which is why the VOUCHER's own date is not judged for these two, and must
	// not be: a cheque written in April and cleared in August is the ordinary
	// case, and 0045's trigger requires bank_date >= voucher_date, so judging the
	// voucher would refuse the feature at every company that has locked a period.
	// See storedCutoffDateForRequest.
	//
	// ⚠️ THEIR OWN DATE — the `bank_date` — IS JUDGED, and was not when this entry
	// was first written: peekBankDate reads the asserted one and
	// storedBankDateForLeg the stored one, both ends, exactly as the voucher
	// routes above. That hole is the one routeAction's comment on this prefix
	// asserted was already closed; see auth_bank_date_cutoff_test.go.
	"PUT /v1/companies/{companyID}/bank-reconciliation/{ledgerID}/{voucherID}/{legNo}": "" +
		"writes only bank_reconciliation; no ledger figure moves (its bank_date is judged separately)",
	"DELETE /v1/companies/{companyID}/bank-reconciliation/{ledgerID}/{voucherID}/{legNo}": "" +
		"writes only bank_reconciliation; no ledger figure moves (its bank_date is judged separately)",

	// Attaching further deductions to a deposit that is already recorded
	// (register row D1). masters.AllocateToRecordedChallan writes only
	// tds_challan_allocation rows: no voucher_version, no posting, no ledger
	// figure moves, and it cannot take any voucher into or out of a locked
	// period — which is why it is here rather than in
	// existingVouchersNamedByRequest.
	//
	// ⚠️ IT IS NOT UNJUDGED. Its own date — the STORED deposit_date of the challan
	// in the path — IS read and judged, by storedChallanDepositDate, exactly as
	// the bank-recon routes' bank_date is. The entry says "not moving a voucher",
	// never "no cutoff applies"; auth_tds_challan_allocate_test.go drives the
	// refusal and its control.
	//
	// ⛔ AND THE DEDUCTION'S OWN DATE IS DELIBERATELY NOT JUDGED, for the reason
	// storedCutoffDateForRequest gives for the bank leg: tax is DEDUCTED before it
	// is DEPOSITED, always, so every allocation names a voucher older than the
	// deposit. Judging it would refuse the feature at every company that has
	// locked a filed month while protecting nothing — the voucher is not restated
	// and no figure of its period moves.
	"POST /v1/companies/{companyID}/tds-challans/{challanID}/allocations": "" +
		"writes only tds_challan_allocation; no voucher is restated (its stored deposit_date is " +
		"judged separately, by storedChallanDepositDate)",

	// Raising an e-way bill / an e-invoice AGAINST a voucher. Both are siblings
	// of the ledger, not callers of it (archtest pins internal/ewaybill and
	// internal/einvoice away from posting), so neither can restate or remove a
	// voucher. The e-invoice's statutory consequence — the freeze — travels back
	// through POST /vouchers/{id}/external-reference above and is governed there.
	"POST /v1/companies/{companyID}/e-way-bills": "cannot post; raises a document ABOUT a voucher",
	"POST /v1/companies/{companyID}/e-invoices":  "cannot post; the freeze rides external-reference",

	// The bank STATEMENT import. The {ledgerID} in the path names a MASTER, not a
	// dated document, and bankstatement is a write service that stores the bank's
	// own rows — it posts no voucher and restates none, so nothing crosses the
	// cutoff. (Preview writes nothing at all.)
	"POST /v1/companies/{companyID}/bank-statement/{ledgerID}/preview": "names a ledger master; writes nothing",
	"POST /v1/companies/{companyID}/bank-statement/{ledgerID}/import":  "names a ledger master; posts no voucher",

	// Closing an order. orders is pinned by archtest to the db layer only: an
	// order is a committed quantity, never a movement, so closing one posts
	// nothing. (The cutoff is deliberately inert on orders in BOTH directions —
	// see routeAction — so there is no asymmetry to exploit.)
	"PUT /v1/companies/{companyID}/sales-orders/{orderID}/close":    "orders post nothing",
	"PUT /v1/companies/{companyID}/purchase-orders/{orderID}/close": "orders post nothing",

	// Masters and the admin surface: named by id, but not dated ledger
	// documents. Restating a ledger's OPENING balance does move a balance that
	// filed periods depend on — but it is a masters write with no date on the
	// wire and no voucher, so it is a different control's problem, not this
	// one's. Recorded here rather than left to silence.
	"PUT /v1/companies/{companyID}/ledgers/{ledgerID}/opening":    "masters write; no voucher, no date",
	"PUT /v1/companies/{companyID}/cost-categories/{categoryID}":  "master rename",
	"PUT /v1/companies/{companyID}/cost-centres/{centreID}":       "master rename",
	"PUT /v1/companies/{companyID}/deductor-status/{taxYear}":     "not a ledger entry",
	"PUT /v1/companies/{companyID}/boms/{bomID}":                  "a recipe is undated",
	"PUT /v1/companies/{companyID}/sales-orders/{orderID}":        "orders post nothing",
	"PUT /v1/companies/{companyID}/purchase-orders/{orderID}":     "orders post nothing",
	"PUT /v1/companies/{companyID}/members/{userID}":              "admin surface",
	"DELETE /v1/companies/{companyID}/members/{userID}":           "admin surface",
	"DELETE /v1/companies/{companyID}/members/invites/{inviteID}": "admin surface",
	"PUT /v1/companies/{companyID}/security-levels/{levelID}":     "admin surface (this is where the cutoff itself is set)",
	"DELETE /v1/companies/{companyID}/security-levels/{levelID}":  "admin surface",
}

// TestEveryWriteRouteNamingAnExistingDocumentIsAccountedFor is the gate that is
// meant to stop a round 4.
//
// It walks the REAL router and, for every WRITE route under {companyID} whose
// pattern carries an existing document's id, requires that the route either
//
//	(a) is recognised by existingVouchersNamedByRequest — i.e. the PDP reads the
//	    stored date and judges the source end of the move; or
//	(b) appears in writeRoutesNamingADocumentButNotMovingIt with a stated reason.
//
// A new write route that takes a {voucherID} therefore cannot be mounted in
// silence: it fails HERE, naming itself, and whoever adds it has to decide which
// of the two it is. That is the structural answer to "each previous round fixed
// what it was shown".
func TestEveryWriteRouteNamingAnExistingDocumentIsAccountedFor(t *testing.T) {
	srv := &Server{}
	srv.routes() // the real router; no DB needed to enumerate routes.

	companyID := uuid.NewString()
	const prefix = "/v1/companies/{companyID}"

	walked, recognised := 0, 0
	err := chi.Walk(srv.router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if !strings.HasPrefix(route, prefix) {
			return nil
		}
		switch method {
		case http.MethodPost, http.MethodPut, http.MethodDelete:
		default:
			return nil
		}
		// Only routes that NAME something already stored are in this class. A
		// collection POST creates; there is no source end to judge.
		rest := strings.TrimPrefix(route, prefix)
		if !strings.Contains(rest, "{") &&
			rest != "/sync/commit" && rest != "/import/tally-xml" {
			return nil
		}
		walked++

		path, rctx := synthPath(route, companyID)
		req := httptest.NewRequest(method, path, nil).
			WithContext(context.WithValue(context.Background(), chi.RouteCtxKey, rctx))
		act, _, _, mapped := routeAction(req)
		if !mapped {
			return nil // TestEveryCompanyRouteIsMappedAndInVocabulary owns that failure.
		}
		if !act.IsWrite() {
			return nil
		}

		// The two body-named routes cannot be probed with an empty body (the ids
		// live in the payload), so they are recognised structurally by the switch
		// in existingVouchersNamedByRequest. Assert that switch arm is reachable
		// rather than that a nil body yields names.
		if rest == "/sync/commit" || rest == "/import/tally-xml" {
			recognised++
			return nil
		}

		named := existingVouchersNamedByRequest(req, act)
		key := method + " " + route
		reason, allowed := writeRoutesNamingADocumentButNotMovingIt[key]
		switch {
		case !named.empty():
			recognised++
			if allowed {
				t.Errorf("route %q is BOTH read for a stored date and listed as harmless (%q) — one of "+
					"the two is wrong", key, reason)
			}
		case allowed:
			// Accounted for, with a stated reason.
		default:
			t.Errorf("WRITE route %q names an existing document but the PDP reads NO stored date for "+
				"it, and it is not listed in writeRoutesNamingADocumentButNotMovingIt.\n"+
				"This is the shape that has now cost three rounds: a write that names an ALREADY-DATED "+
				"ledger document is judged only on the date it SENDS, so it can lift that document out "+
				"of a locked period or delete it out of one. Either add an arm to "+
				"existingVouchersNamedByRequest so the stored date is judged, or add this route to the "+
				"allowlist WITH the reason it cannot move a ledger entry across the cutoff.", key)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk router: %v", err)
	}
	if walked < 15 {
		t.Fatalf("walked only %d id-bearing write routes; the router or the prefix changed and this "+
			"gate is no longer looking at anything", walked)
	}
	// The four doors of the class: PUT /vouchers/{class}/{id}, DELETE
	// /vouchers/{id}, /sync/commit, /import/tally-xml. The PUT is mounted once
	// per voucher class, so the count is (classes + 3).
	if recognised < 4 {
		t.Fatalf("only %d write routes are recognised by existingVouchersNamedByRequest; the four "+
			"doors of this class must all be", recognised)
	}
}

// TestExistingVouchersNamedByRequestIgnoresReads pins the hot-path decision: a
// Display or a Print names a voucher on nearly every screen in the product, and
// none of them may cost a store read.
func TestExistingVouchersNamedByRequestIgnoresReads(t *testing.T) {
	companyID := uuid.NewString()
	for _, tc := range []struct {
		method, path string
		act          authz.Action
	}{
		{http.MethodGet, "/v1/companies/" + companyID + "/vouchers/" + uuid.NewString(), authz.Display},
		{http.MethodGet, "/v1/companies/" + companyID + "/print/vouchers/journal/" + uuid.NewString(), authz.Print},
	} {
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("companyID", companyID)
		req := httptest.NewRequest(tc.method, tc.path, nil).
			WithContext(context.WithValue(context.Background(), chi.RouteCtxKey, rctx))
		if named := existingVouchersNamedByRequest(req, tc.act); !named.empty() {
			t.Errorf("existingVouchersNamedByRequest(%s %s, %s) named %d vouchers, want none — a read "+
				"must never trigger the store read", tc.method, tc.path, tc.act, len(named.ids))
		}
	}
}
