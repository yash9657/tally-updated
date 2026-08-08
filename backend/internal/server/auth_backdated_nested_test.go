package server

// auth_backdated_nested_test.go — THE BACK-DATED CUTOFF MUST NOT BE BYPASSABLE
// BY MOVING THE DATE ONE LEVEL DOWN.
//
// ===========================================================================
// WHY THIS FILE EXISTS: THE FIX IN auth_backdated_spelling_test.go WAS REAL AND
// WAS NOT ENOUGH
// ===========================================================================
// That file closed the loose-spelling door and it HOLDS: five respellings of a
// back-date 403 with nothing written and the refusal names the cutoff. Then a
// browser re-drive put a voucher into the locked period anyway, on the same
// company, at the same level, under the same cutoff, with a strictly-spelled
// date — through a DIFFERENT route:
//
//	SYNC     POST /sync/commit  intents[0].payload.voucher_date=2026-05-01 -> 200
//	         {"outcomes":[{"detail":"created","final_voucher_no":"6","status":"COMMITTED"}]}
//	CONTROL  POST /vouchers/journal        voucher_date=2026-05-01         -> 403
//	         {"error":"voucher date is before the back-dated cutoff of 2026-07-01"}
//
// One door refused and the other COMMITTED, and the committed voucher was in the
// books with matched Dr Rent / Cr Cash. /sync/commit is routed as a write, so
// every other control applied to it; the peek simply could not SEE the date,
// because a sync body carries a whole voucher request nested under
// `intents[].payload` and the peek read one key at the top level.
//
// This is the FOURTH time this one control has been inert on a route (the pay
// run's wage_month, the challan's deposit_date, the loose spelling, now the
// nested payload), so the tests here are written against the CLASS rather than
// against /sync/commit: any body that carries a voucher date at any depth, plus
// the one write body that is not JSON at all (the Tally XML import, which posts
// every <VOUCHER> in an envelope through ledger.Post dated by its own <DATE>).
//
// WHAT IT COSTS. A cutoff is set the day a quarter's returns go to the portals.
// A voucher inserted into that quarter afterwards changes what the books say
// about a period whose GSTR-1, GSTR-3B and 26Q are already filed. Through
// /sync/commit an offline node can do it in bulk and the outcome list makes it
// look like ordinary catch-up traffic; through /import/tally-xml one request can
// carry ten thousand of them.

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// UNIT: the peek itself. This is the leg that fails the moment somebody
// restores a top-level-only probe.
// ---------------------------------------------------------------------------

// TestPeekVoucherDateSeesNestedAndXMLVoucherDates pins all four directions of
// the peek's contract at once, because they are in tension and a fix that gets
// three of them right is the shape of every previous round of this defect:
//
//   - a NESTED date is READ (the bypass the re-drive drove);
//   - a batch is judged by its EARLIEST member (one allowed intent must not
//     carry a back-dated one in with it);
//   - a nested UNREADABLE date refuses (the round-1 rule, at depth);
//   - a body with NO voucher date anywhere still returns nil (master creates and
//     ledger-only imports must keep working under a cutoff — round 1's mutation
//     proof caught exactly this).
func TestPeekVoucherDateSeesNestedAndXMLVoucherDates(t *testing.T) {
	// The earliest date a company could plausibly set as a cutoff. Anything the
	// peek returns for an unreadable date must be before it.
	earliestPlausibleCutoff := time.Date(1900, time.January, 1, 0, 0, 0, 0, time.UTC)
	may1 := time.Date(2026, time.May, 1, 0, 0, 0, 0, time.UTC)

	// syncBody is the real /sync/commit shape: syncIntentReq.Payload IS a
	// voucherReq, so the voucher date sits at intents[N].payload.voucher_date.
	syncBody := func(dates ...string) string {
		var b strings.Builder
		b.WriteString(`{"intents":[`)
		for i, d := range dates {
			if i > 0 {
				b.WriteString(",")
			}
			// The key is the intent's INDEX, not the date: `d` arrives as a raw JSON
			// value (quoted, or a number, or an object) so that this builder can
			// send wrong-typed dates, and splicing it into a string field would
			// make the whole body invalid JSON — which peekVoucherDate would then
			// answer nil to for the ordinary "not JSON" reason and the test would
			// pass for the wrong reason.
			b.WriteString(`{"idempotency_key":"k` + strconv.Itoa(i) + `","client_voucher_id":"` +
				uuid.NewString() + `","op":"CREATE","class":"Journal",` +
				`"hlc":{"wall_millis":1,"counter":0,"node_id":"n1"},"node_id":"n1","base_version":0,` +
				`"payload":{"voucher_date":` + d + `,"narration":"offline","postings":[]}}`)
		}
		b.WriteString(`]}`)
		return b.String()
	}

	t.Run("a nested voucher date is READ", func(t *testing.T) {
		// ⛔ THIS IS THE DRIVEN BYPASS. Before the fix this returned nil and nil
		// means "the cutoff does not apply".
		got := peekVoucherDate([]byte(syncBody(`"2026-05-01"`)))
		if got == nil {
			t.Fatalf("peekVoucherDate(sync body with payload.voucher_date=2026-05-01) = nil — nil means " +
				"the back-dated cutoff DOES NOT APPLY, so an offline node commits straight into a filed " +
				"period. This is the exact body a browser re-drive committed with (HTTP 200, " +
				"status COMMITTED) while POST /vouchers/journal with the same date got 403")
		}
		if !got.Equal(may1) {
			t.Fatalf("peekVoucherDate(nested 2026-05-01) = %s, want 2026-05-01 — the PDP must judge the "+
				"date the LEDGER will store, and buildVoucherInput passes payload.voucher_date straight "+
				"through", got.Format("2006-01-02"))
		}
	})

	t.Run("a batch is judged by its EARLIEST member", func(t *testing.T) {
		// Only ONE of the three intents is back-dated. A batch is only as
		// permitted as its most-refused member: sync.Commit commits them together
		// and there is no partial authorization to express.
		for _, body := range []string{
			syncBody(`"2026-08-20"`, `"2026-05-01"`, `"2026-09-02"`), // back-date in the middle
			syncBody(`"2026-05-01"`, `"2026-08-20"`),                 // first
			syncBody(`"2026-08-20"`, `"2026-05-01"`),                 // last
		} {
			got := peekVoucherDate([]byte(body))
			if got == nil || !got.Equal(may1) {
				t.Errorf("peekVoucherDate(batch containing 2026-05-01) = %v, want 2026-05-01 — the "+
					"cutoff must judge the earliest intent in the batch, or one allowed intent carries "+
					"a back-dated one in with it and the whole request answers 200 COMMITTED", got)
			}
		}
	})

	t.Run("a nested unreadable date refuses", func(t *testing.T) {
		for _, body := range []string{
			syncBody(`"2026-5-1"`),                 // no zero-padding: Postgres reads it, time.Parse does not
			syncBody(`"2026-05-01T00:00:00Z"`),     // RFC 3339
			syncBody(`"20260501"`),                 // ISO basic
			syncBody(`20260501`),                   // wrong JSON type: a number
			syncBody(`{"y":2026}`),                 // wrong JSON type: an object
			syncBody(`"2026-08-20"`, `"notadate"`), // one good, one junk -> the batch refuses
			`[{"voucher_date":"2026-5-31"}]`,       // a bare array: nested, and unreadable
		} {
			got := peekVoucherDate([]byte(body))
			if got == nil {
				t.Errorf("peekVoucherDate(%s) = nil — cutoffDeny SKIPS a nil date, so a nested date it "+
					"cannot read is presented to the PDP with the cutoff DISABLED, while Postgres "+
					"accepts several of these spellings for the DATE column", body)
				continue
			}
			if !got.Before(earliestPlausibleCutoff) {
				t.Errorf("peekVoucherDate(%s) = %s, want a date before every settable cutoff (%s)",
					body, got.Format("2006-01-02"), earliestPlausibleCutoff.Format("2006-01-02"))
			}
		}
	})

	t.Run("a nested body with no date still returns nil", func(t *testing.T) {
		// ⛔ THE HALF THAT IS USUALLY MISSING. This peek is the fall-through for
		// EVERY write in the product. A body that nests things but dates nothing
		// must stay nil, or the fix locks these writes out of any company that has
		// set a cutoff.
		for _, body := range []string{
			// a sync commit of an intent that carries no date at all
			`{"intents":[{"op":"CREATE","class":"Journal","payload":{"narration":"x","postings":[]}}]}`,
			// a sync body with an explicitly null / empty date
			syncBody(`null`),
			syncBody(`""`),
			// an item invoice: nested arrays and objects, dates nowhere
			`{"postings":[{"ledger_id":"a","dr_cr":"D","amount":"1.0000"}],` +
				`"inventory":[{"item_id":"b","qty":"1","rate":"1.0000"}],"gst":{"place_of_supply":"29"}}`,
			// a price list: nested rows with a date-shaped field that is NOT a voucher date
			`{"effective_from":"2026-05-01","rows":[{"qty_from":"1","rate":"9.0000"}]}`,
			// a bank statement import: a whole CSV inside a JSON string
			`{"file_name":"apr.csv","content":"date,desc\n01-05-2026,rent\n","mapping":{"txn_date":"date"}}`,
		} {
			if got := peekVoucherDate([]byte(body)); got != nil {
				t.Errorf("peekVoucherDate(%s) = %s, want nil — this body carries no voucher date to "+
					"back-date. Refusing it locks master creates, price lists and statement imports out "+
					"of every company that has set a cutoff, which is an outage and not a control",
					body, got.Format("2006-01-02"))
			}
		}
	})

	t.Run("a correctly spelled top-level date is still read", func(t *testing.T) {
		// Round 1's behaviour, unchanged: the walk must not have cost the ordinary
		// F-key POST its date.
		got := peekVoucherDate([]byte(`{"voucher_date":"2026-08-15","postings":[]}`))
		want := time.Date(2026, time.August, 15, 0, 0, 0, 0, time.UTC)
		if got == nil || !got.Equal(want) {
			t.Fatalf("peekVoucherDate(top-level 2026-08-15) = %v, want %s", got, want.Format("2006-01-02"))
		}
	})

	t.Run("a date buried deeper than the walk goes refuses rather than being skipped", func(t *testing.T) {
		// Burying the date past voucherDatePeekMaxDepth must not be a way to hide
		// it: "I stopped looking" and "there is nothing here" are the two answers
		// this whole peek exists to keep apart. Nothing the API accepts nests this
		// deep, so the only caller who ever sees this is somebody probing.
		deep := `{"voucher_date":"2026-05-01"}`
		for i := 0; i < 200; i++ {
			deep = `{"a":` + deep + `}`
		}
		got := peekVoucherDate([]byte(deep))
		if got == nil {
			t.Fatalf("peekVoucherDate(a voucher_date nested 200 deep) = nil — nil means the cutoff does "+
				"not apply, so the depth bound would itself be the bypass (bound = %d)",
				voucherDatePeekMaxDepth)
		}
		if !got.Before(earliestPlausibleCutoff) {
			t.Fatalf("peekVoucherDate(nested 200 deep) = %s, want a date before every settable cutoff — "+
				"a body the peek gave up reading must be REFUSED", got.Format("2006-01-02"))
		}
	})
}

// TestPeekVoucherDateSeesTheTallyXMLImport covers the OTHER route the peek could
// not see: POST /import/tally-xml, whose body is not JSON at all.
//
// handleImportTallyXML hands the body to migration.Importer, which posts every
// <VOUCHER> through ledger.Post dated by its own <DATE>. The round-1 peek saw a
// non-JSON body and returned nil — "a voucher write cannot hide in such a body"
// — which was true of a multipart upload and false of the one endpoint that
// ingests thousands of dated vouchers per request.
func TestPeekVoucherDateSeesTheTallyXMLImport(t *testing.T) {
	earliestPlausibleCutoff := time.Date(1900, time.January, 1, 0, 0, 0, 0, time.UTC)

	env := func(inner string) string {
		return `<?xml version="1.0" encoding="UTF-8"?><ENVELOPE><HEADER><TALLYREQUEST>Import` +
			`</TALLYREQUEST></HEADER><BODY><DESC><STATICVARIABLES><SVCURRENTCOMPANY>Acme` +
			`</SVCURRENTCOMPANY></STATICVARIABLES></DESC><DATA>` + inner + `</DATA></BODY></ENVELOPE>`
	}
	voucher := func(date string) string {
		return `<TALLYMESSAGE><VOUCHER VCHTYPE="Journal" ACTION="Create"><DATE>` + date + `</DATE>` +
			`<VOUCHERNUMBER>1</VOUCHERNUMBER><LEDGERENTRIES.LIST><LEDGERNAME>Rent</LEDGERNAME>` +
			`<ISDEEMEDPOSITIVE>Yes</ISDEEMEDPOSITIVE><AMOUNT>-1000</AMOUNT></LEDGERENTRIES.LIST>` +
			`<LEDGERENTRIES.LIST><LEDGERNAME>Cash</LEDGERNAME><ISDEEMEDPOSITIVE>No</ISDEEMEDPOSITIVE>` +
			`<AMOUNT>1000</AMOUNT></LEDGERENTRIES.LIST></VOUCHER></TALLYMESSAGE>`
	}

	t.Run("a dated voucher in an envelope is READ", func(t *testing.T) {
		// Tally's own YYYYMMDD spelling, which TallyDateToISO converts and the
		// importer then posts.
		got := peekVoucherDate([]byte(env(voucher("20260501"))))
		want := time.Date(2026, time.May, 1, 0, 0, 0, 0, time.UTC)
		if got == nil || !got.Equal(want) {
			t.Fatalf("peekVoucherDate(tally envelope, <DATE>20260501</DATE>) = %v, want 2026-05-01 — nil "+
				"means the cutoff does not apply, and this route posts EVERY voucher in the envelope "+
				"through ledger.Post dated by that element. An operator who cannot post one back-dated "+
				"journal could import ten thousand", got)
		}
	})

	t.Run("the earliest voucher in the envelope is the one judged", func(t *testing.T) {
		got := peekVoucherDate([]byte(env(voucher("20260820") + voucher("20260501") + voucher("20260902"))))
		want := time.Date(2026, time.May, 1, 0, 0, 0, 0, time.UTC)
		if got == nil || !got.Equal(want) {
			t.Fatalf("peekVoucherDate(envelope with 2026-08-20, 2026-05-01, 2026-09-02) = %v, want "+
				"2026-05-01 — the importer posts them all in one request, so the batch is only as "+
				"permitted as its earliest voucher", got)
		}
	})

	t.Run("an unreadable date in an envelope refuses", func(t *testing.T) {
		for _, body := range []string{
			env(voucher("2026-5-1")),                       // hyphenated: TallyDateToISO passes it through, Postgres reads it
			env(voucher("1 May 2026")),                     // spelled out
			env(voucher("")),                               // no date at all on a VOUCHER element
			env(voucher("20260820") + voucher("2026-5-1")), // one good, one loose -> refuse the lot
			`<ENVELOPE><BODY><DATA><TALLYMESSAGE><VOUCHER`, // truncated envelope: unparseable
		} {
			got := peekVoucherDate([]byte(body))
			if got == nil {
				t.Errorf("peekVoucherDate(%.60s...) = nil — a Tally date the PDP cannot read is a date "+
					"the importer may well post (TallyDateToISO returns a hyphenated value UNCHANGED and "+
					"Postgres accepts \"2026-5-1\" in a DATE column), so the cutoff must refuse rather "+
					"than skip", body)
				continue
			}
			if !got.Before(earliestPlausibleCutoff) {
				t.Errorf("peekVoucherDate(%.60s...) = %s, want a date before every settable cutoff",
					body, got.Format("2006-01-02"))
			}
		}
	})

	t.Run("a masters-only import and a non-envelope body still return nil", func(t *testing.T) {
		// ⛔ THE CONTROL. An envelope of LEDGER masters dates nothing, and neither
		// does a CSV or a multipart preamble. Refusing these would mean a company
		// that has locked a period can no longer import a chart of accounts.
		for _, body := range []string{
			env(`<TALLYMESSAGE><LEDGER NAME="Rent"><PARENT>Indirect Expenses</PARENT>` +
				`<OPENINGBALANCE>-1000</OPENINGBALANCE></LEDGER></TALLYMESSAGE>`),
			env(``), // an envelope with no messages
			`<html><body>not tally</body></html>`,
			`date,description,amount` + "\n" + `01-05-2026,rent,1000`,
			`--boundary\r\nContent-Disposition: form-data`,
			`not json at all`,
			``,
		} {
			if got := peekVoucherDate([]byte(body)); got != nil {
				t.Errorf("peekVoucherDate(%.60s...) = %s, want nil — nothing here is a dated voucher, "+
					"and this peek is the fall-through for EVERY write in the product",
					body, got.Format("2006-01-02"))
			}
		}
	})
}

// ---------------------------------------------------------------------------
// HTTP: the driven bypass, through the real router, the real seeded Owner level
// and a real cutoff set through the real admin API.
// ---------------------------------------------------------------------------

// TestBackDatedCutoffRefusesASyncCommitIntoALockedPeriod is the gate for the
// re-driven defect. The legs, and why each is load-bearing:
//
//	CONTROL   POST /vouchers/journal  voucher_date 2026-05-01        -> 403
//	          Proves the cutoff is live on THIS company; without it every 403
//	          below could be any other denial and the test proves nothing.
//	ATTACK    POST /sync/commit       payload.voucher_date 2026-05-01 -> MUST 403
//	          This is the bypass that returned 200 COMMITTED on the wire.
//	NOTHING   GET /day-book over the locked month                    -> no probe
//	          A 403 that still wrote is the defect this whole audit keeps
//	          finding; the status code alone is not the assertion.
//	BATCH     one allowed + one back-dated intent                    -> MUST 403
//	          AND the allowed one must not have landed either.
//	ALLOWED   POST /sync/commit       payload.voucher_date 2026-08-20 -> MUST 200
//	          ⛔ THE CONTROL. A "fix" that refused every sync would pass every
//	          leg above and break offline catch-up entirely.
func TestBackDatedCutoffRefusesASyncCommitIntoALockedPeriod(t *testing.T) {
	s := payrollServer(t)
	f := newSpellingFixture(t, s)

	levelID := aclOwnerLevelID(t, s, f.base, f.ownerTok)
	const cutoff = "2026-07-01"
	aclSetCutoff(t, s, f.base, levelID, f.ownerTok, cutoff)

	// syncIntent builds ONE offline CREATE of a balanced journal, dated `date`.
	// It carries a narration stamped with `tag` so the day-book leg below can
	// name what landed rather than only count rows.
	syncIntent := func(date, tag string) map[string]any {
		node := "node-" + tag
		return map[string]any{
			"idempotency_key":   "sync-cutoff-" + tag + "-" + uuid.NewString()[:8],
			"client_voucher_id": uuid.NewString(),
			"op":                "CREATE",
			"class":             "Journal",
			"node_id":           node,
			"hlc":               map[string]any{"wall_millis": 1, "counter": 0, "node_id": node},
			"base_version":      0,
			"payload": map[string]any{
				"voucher_date": date,
				"narration":    "sync cutoff probe " + tag,
				"created_by":   "sync-cutoff-test",
				"postings": []any{
					map[string]any{"ledger_id": f.expense, "dr_cr": "D", "amount": "1000.0000"},
					map[string]any{"ledger_id": f.bank, "dr_cr": "C", "amount": "1000.0000"},
				},
			},
		}
	}

	// ------------------------------------------------------------------ CONTROL
	rec, body := aclRequest(t, s, http.MethodPost, f.base+"/vouchers/journal", f.ownerTok,
		f.journal("2026-05-01", "SYNC-CONTROL"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("CONTROL: POST /vouchers/journal voucher_date=2026-05-01 under a %s cutoff = %d, want "+
			"403 — the cutoff is not live on this company, so every assertion below proves nothing (%s)",
			cutoff, rec.Code, rec.Body.String())
	}

	// ------------------------------------------------------------------- ATTACK
	rec, body = aclRequest(t, s, http.MethodPost, f.base+"/sync/commit", f.ownerTok,
		map[string]any{"intents": []any{syncIntent("2026-05-01", "ATTACK")}})
	if rec.Code != http.StatusForbidden {
		t.Errorf("ATTACK: POST /sync/commit with intents[0].payload.voucher_date=2026-05-01 under a %s "+
			"cutoff = %d, want 403 — the SAME date on POST /vouchers/journal is refused (the CONTROL "+
			"above). /sync/commit is routed as a write, so every other control applies to it; the PDP "+
			"peek simply could not SEE a date nested under intents[].payload and a nil date means the "+
			"cutoff does not apply. A browser re-drive committed exactly this body with HTTP 200 and "+
			"status COMMITTED (%s)", cutoff, rec.Code, rec.Body.String())
	} else {
		// The reason must be the CUTOFF's and it must NAME the boundary, exactly as
		// the ordinary voucher POST's does — an operator who is told only that
		// "something is locked" has to find the boundary by resubmitting.
		msg, _ := body["error"].(string)
		if !strings.Contains(msg, cutoff) {
			t.Errorf("ATTACK refusal reason = %q, want it to name the cutoff date %s — an offline node's "+
				"operator sees this string and nothing else; without the boundary they cannot tell a "+
				"period lock from a missing grant (%s)", msg, cutoff, rec.Body.String())
		}
	}

	// ------------------------------------------------------------------ NOTHING
	// A 403 that still wrote is this project's signature failure. The day book
	// over the locked month is the DB's own answer.
	if landed := syncProbesInDayBook(t, s, f, "2026-05-01", "2026-05-31"); len(landed) > 0 {
		t.Errorf("the refused sync commit still WROTE: the day book for May 2026 contains %v — the "+
			"request answered 403 and the voucher is in the books inside the locked period, which is "+
			"worse than a plain bypass because the audit trail says the write was refused", landed)
	}

	// -------------------------------------------------------------------- BATCH
	// ⚠️ A BATCH IS ONLY AS PERMITTED AS ITS MOST-REFUSED MEMBER. One allowed
	// intent must not carry a back-dated one in with it.
	rec, body = aclRequest(t, s, http.MethodPost, f.base+"/sync/commit", f.ownerTok,
		map[string]any{"intents": []any{
			syncIntent("2026-08-20", "BATCH-OK"),
			syncIntent("2026-05-01", "BATCH-BAD"),
		}})
	if rec.Code != http.StatusForbidden {
		t.Errorf("BATCH: POST /sync/commit with one post-cutoff and one back-dated intent under a %s "+
			"cutoff = %d, want 403 — sync.Commit commits its intents TOGETHER and answers one status "+
			"for the request, so a peek that judged only the first (or the last, or the newest) intent "+
			"lets a back-dated one ride in behind an allowed one (%s)", cutoff, rec.Code, rec.Body.String())
	}
	if landed := syncProbesInDayBook(t, s, f, "2026-05-01", "2026-05-31"); len(landed) > 0 {
		t.Errorf("the refused BATCH still wrote into the locked period: %v", landed)
	}
	if landed := syncProbesInDayBook(t, s, f, "2026-08-01", "2026-08-31"); len(landed) > 0 {
		t.Errorf("the refused BATCH committed its ALLOWED member anyway: %v — a refused request must "+
			"write nothing at all, or an operator retrying it duplicates whatever did land", landed)
	}

	// ------------------------------------------------------------------ ALLOWED
	// ⛔ THE CONTROL. August is AFTER the cutoff and must still commit, or the fix
	// is a blanket refusal of offline sync and no node can ever catch up.
	rec, body = aclRequest(t, s, http.MethodPost, f.base+"/sync/commit", f.ownerTok,
		map[string]any{"intents": []any{syncIntent("2026-08-20", "ALLOWED")}})
	if rec.Code != http.StatusOK {
		t.Fatalf("ALLOWED: POST /sync/commit voucher_date=2026-08-20 under a %s cutoff = %d, want 200 — "+
			"a date AFTER the cutoff must still commit. Refusing it means the nested peek now refuses "+
			"every sync and offline nodes can never reconcile (%s)", cutoff, rec.Code, rec.Body.String())
	}
	outcomes, _ := body["outcomes"].([]any)
	if len(outcomes) != 1 {
		t.Fatalf("ALLOWED: want 1 outcome, got %v (%s)", body["outcomes"], rec.Body.String())
	}
	o, _ := outcomes[0].(map[string]any)
	if got, _ := o["status"].(string); got != "COMMITTED" {
		t.Errorf("ALLOWED: outcome status = %q, want COMMITTED (%s)", got, rec.Body.String())
	}
	if landed := syncProbesInDayBook(t, s, f, "2026-08-01", "2026-08-31"); len(landed) != 1 {
		t.Errorf("ALLOWED: the day book for August 2026 shows %v, want exactly the one committed probe "+
			"— a 200 that wrote nothing is the other half of this project's signature failure", landed)
	}
}

// syncProbesInDayBook returns the narrations of this test's sync probes that are
// in the day book for [from, to]. It reads through the ordinary report route, so
// what it reports is what an operator would see on the Day Book screen.
func syncProbesInDayBook(t *testing.T, s *Server, f spellingFixture, from, to string) []string {
	t.Helper()
	rec, body := aclRequest(t, s, http.MethodGet,
		f.base+"/day-book?from="+from+"&to="+to, f.ownerTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /day-book?from=%s&to=%s = %d, want 200 — without the read-back this test can only "+
			"assert status codes, and a 403 that still wrote would pass (%s)", from, to, rec.Code,
			rec.Body.String())
	}
	entries, _ := body["entries"].([]any)
	found := []string{}
	for _, raw := range entries {
		e, _ := raw.(map[string]any)
		n, _ := e["narration"].(string)
		if strings.HasPrefix(n, "sync cutoff probe ") {
			d, _ := e["date"].(string)
			found = append(found, n+" dated "+d)
		}
	}
	return found
}
