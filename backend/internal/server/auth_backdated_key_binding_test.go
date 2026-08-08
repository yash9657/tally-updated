package server

// auth_backdated_key_binding_test.go — THE BACK-DATED CUTOFF MUST BIND THE DATE
// THE WAY THE HANDLER BINDS IT.
//
// ===========================================================================
// WHY THIS FILE EXISTS, AND WHY IT IS THE FIFTH TIME
// ===========================================================================
// This one control has now been inert five times, always for the same underlying
// reason and never twice for the same input:
//
//	1. the pay run    the date was spelled `wage_month`     (peek could not FIND it)
//	2. the challan    the date was spelled `deposit_date`   (peek could not FIND it)
//	3. loose dates    "2026-5-31", "20260531"               (peek could not READ it)
//	4. nested bodies  intents[].payload.voucher_date        (peek looked at ONE depth)
//	5. THIS FILE      {"VOUCHER_DATE":...}                  (peek matched the key
//	                                                        BYTE-FOR-BYTE while
//	                                                        encoding/json binds it
//	                                                        CASE-INSENSITIVELY)
//
// Verbatim, on the wire, against a company whose Owner level had
// BackdatedCutoff = 2026-07-01 and AFTER rounds 1–4 had all shipped:
//
//	POST /vouchers/journal  {"VOUCHER_DATE":"2026-05-01",...} -> 201 voucher_no 6
//	POST /vouchers/journal  {"Voucher_Date":"2026-05-01",...} -> 201 voucher_no 7
//	POST /vouchers/journal  {"voucher_DATE":"2026-05-01",...} -> 201 voucher_no 8
//	POST /sync/commit  intents[0].payload.Voucher_Date        -> 200 COMMITTED
//	CONTROL POST /vouchers/journal {"voucher_date":"2026-05-01"} -> 403
//	        {"error":"voucher date is before the back-dated cutoff of 2026-07-01"}
//
// One of those was driven from a real browser with a real passkey step-up, so it
// is not a curl-only artefact. Five back-dated vouchers are in the operator's own
// May 2026 Day Book, inside a quarter whose GSTR-1, GSTR-3B and 26Q are filed.
//
// ⛔ THE DEFECT IS NOT "CASE". IT IS THAT THE PEEK WAS A SECOND IMPLEMENTATION OF
// THE DECODER. Any question the peek answers differently from encoding/json is a
// bypass, because the PDP then authorizes a body that is not the body the writer
// will store. Case was merely the cheapest divergence to type; the tests below are
// therefore written against the CLASS — they DERIVE the expected answer by
// decoding each hostile body into the REAL wire structs (voucherReq,
// syncCommitReq) with the REAL decode call (decodeJSON's json.NewDecoder().
// Decode()), and require the peek to agree. A test that only listed the four
// spellings the re-drive happened to type would be round 5's version of rounds
// 1–4, each of which patched the input it was shown and was followed by another
// door.
//
// WHAT THIS FILE MUST NOT REGRESS, from the earlier rounds — each of these was
// itself caught by a mutation proof and each is the OPPOSITE direction from the
// one above, which is why a fix that gets three of them right is the recurring
// shape of this defect:
//
//	loose dates refuse            a date the PDP cannot read is a date Postgres
//	                              CAN read (round 1)
//	one bad date refuses the LOT  a batch is only as permitted as its most-refused
//	                              member (round 2)
//	the depth bound fails CLOSED  "I stopped looking" must never be spelled like
//	                              "there is nothing here" (round 2)
//	NO date returns nil           this peek is the fall-through for EVERY write in
//	                              the product — ledgers, godowns, units, price
//	                              lists, bill uploads. Refusing a body that simply
//	                              carries no voucher_date refuses every master
//	                              create at any company that has set a cutoff,
//	                              which is an outage and not a control (round 1,
//	                              caught by that round's mutation proof)

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// earliestPlausibleCutoffKB is the earliest date a company could plausibly set as
// a cutoff. Anything the peek returns for a body it refuses must be before it, or
// cutoffDeny would not deny.
var earliestPlausibleCutoffKB = time.Date(1900, time.January, 1, 0, 0, 0, 0, time.UTC)

// ---------------------------------------------------------------------------
// 1. THE TAG PIN. The peek borrows the handler's binding rule by carrying the
//    handler's tag; if the tags drift the borrowing silently stops.
// ---------------------------------------------------------------------------

// TestVoucherDateBindingCannotDriftFromTheHandler is named in voucherDateProbe's
// own doc comment as the thing that keeps the two in step, so it has to exist.
//
// ⚠️ WHY A REFLECTION TEST AND NOT A COMMENT. The whole round-5 fix rests on ONE
// premise: that `voucherDateProbe` asks encoding/json the same question
// `voucherReq` asks it. That premise is carried entirely by a struct tag, which
// is a string, in a different part of a 4,000-line file. Renaming voucherReq's
// field tag — a wire rename, a `,omitempty`, a typo — would leave both structs
// compiling, every other test green, and the cutoff silently switched off for
// every write in the product.
func TestVoucherDateBindingCannotDriftFromTheHandler(t *testing.T) {
	handlerField, ok := reflect.TypeOf(voucherReq{}).FieldByName("VoucherDate")
	if !ok {
		t.Fatalf("voucherReq has no VoucherDate field — the PDP peek binds the cutoff's date through " +
			"voucherDateProbe, whose tag is supposed to BE this field's tag. If the field moved or was " +
			"renamed, find where the handler now reads the date from and point the probe at that")
	}
	probeType := reflect.TypeOf(voucherDateProbe{})
	if probeType.NumField() != 1 {
		t.Fatalf("voucherDateProbe has %d fields, want exactly 1 — it exists to ask encoding/json ONE "+
			"question (\"what did the handler bind as this object's voucher_date?\"); extra fields make it "+
			"a second wire shape that can drift from voucherReq in its own right", probeType.NumField())
	}
	probeField := probeType.Field(0)
	if got, want := probeField.Tag.Get("json"), handlerField.Tag.Get("json"); got != want {
		t.Fatalf("voucherDateProbe's json tag is %q but voucherReq.VoucherDate's is %q — the ENTIRE "+
			"round-5 fix is the claim that these two bind the same key through the same library. With "+
			"different tags the PDP peeks one key while ledger.Post is dated by another, and a nil date "+
			"means the back-dated cutoff DOES NOT APPLY. That is the bypass this file was written for, "+
			"restored by a one-word edit", got, want)
	}
	// The pointer half of *json.RawMessage is load-bearing in its own right: it is
	// what keeps ABSENT (must return nil, or every master create at a company with
	// a cutoff is refused) distinguishable from PRESENT-BUT-NOT-A-STRING (must
	// refuse). A plain string field would collapse both into "".
	if probeField.Type.Kind() != reflect.Pointer {
		t.Errorf("voucherDateProbe's field is %s, want a POINTER — a non-pointer collapses ABSENT into "+
			"the zero value, and ABSENT must return nil (round 1: this peek is the fall-through for every "+
			"write in the product) while a present-but-unreadable value must REFUSE. One value cannot "+
			"carry both answers", probeField.Type)
	}
	if probeField.Type != reflect.TypeOf((*json.RawMessage)(nil)) {
		t.Errorf("voucherDateProbe's field is %s, want *json.RawMessage — the RAW BYTES are what keep "+
			"the wire's own type visible, so `{\"voucher_date\":20260501}` stays PRESENT-AND-UNREADABLE "+
			"instead of decoding into \"\" and handing the cutoff its nil", probeField.Type)
	}
}

// ---------------------------------------------------------------------------
// 2. THE DIFFERENTIAL. Every expectation below is CHECKED against what
//    encoding/json actually binds into the real wire structs.
// ---------------------------------------------------------------------------

// peekWant says what peekVoucherDate must answer for a body, in the three
// currencies cutoffDeny actually understands.
type peekWant int

const (
	// wantNoDate: nil. cutoffDeny returns early on a nil date, so this is the
	// answer that DISABLES the control — legal only when the body asserts no
	// voucher date at all.
	wantNoDate peekWant = iota
	// wantRefuse: a date before every settable cutoff. "Present and unreadable",
	// or "I gave up reading this body".
	wantRefuse
	// wantExactly: the date named in the row.
	wantExactly
)

type keyBindingCase struct {
	name string
	body string
	want peekWant
	date string // for wantExactly
	// why is printed on failure. It says what an operator loses, not what the
	// assertion is.
	why string
}

// TestPeekVoucherDateBindsTheKeyTheHandlerBinds is the round-5 gate, at the unit
// level, plus every property rounds 1–3 established.
//
// ⛔ IT DOES TWO THINGS PER ROW, AND THE SECOND IS THE POINT. The first is the
// ordinary table assertion. The second decodes the SAME body into the REAL wire
// structs with the REAL decode call and requires the peek to be no more
// permissive than what encoding/json bound — because the failure mode this whole
// file exists for is not "the peek got a date wrong", it is "the peek and the
// handler read the same bytes differently". No hand-written expectation can catch
// that class; only asking the other layer can.
func TestPeekVoucherDateBindsTheKeyTheHandlerBinds(t *testing.T) {
	may1 := "2026-05-01"

	cases := []keyBindingCase{
		// ---------------------------------------------------------------- ROUND 5
		// ⛔ THE FOUR SPELLINGS THAT WERE DRIVEN. encoding/json binds an object key
		// to a struct field case-insensitively, so every one of these dated a
		// voucher 2026-05-01 while an exact byte compare saw no date at all.
		{"the key is SHOUTED", `{"VOUCHER_DATE":"2026-05-01","postings":[]}`, wantExactly, may1,
			"this exact body returned 201 with voucher_no 6 on the wire, inside a locked period"},
		{"the key is Title Cased", `{"Voucher_Date":"2026-05-01","postings":[]}`, wantExactly, may1,
			"this exact body returned 201 with voucher_no 7 on the wire"},
		{"one WORD of the key is shouted", `{"voucher_DATE":"2026-05-01","postings":[]}`, wantExactly, may1,
			"this exact body returned 201 with voucher_no 8 on the wire"},
		{"the key is randomly cased", `{"VoUcHeR_dAtE":"2026-05-01","postings":[]}`, wantExactly, may1,
			"encoding/json folds the whole key, so there is no bound on how many spellings exist — " +
				"which is why the peek must not enumerate them"},

		// DUPLICATE KEYS. encoding/json assigns EVERY matching key in turn, so the
		// LAST one wins and the handler stores May. A peek that kept both and chose
		// by Go's randomised map order would pass this test roughly half the time,
		// which is worse than failing it.
		{"duplicate keys, the LAST is the back-dated one",
			`{"voucher_date":"2026-09-01","VOUCHER_DATE":"2026-05-01","postings":[]}`, wantExactly, may1,
			"the handler binds 2026-05-01 (last wins) and the voucher lands in May; a peek that read " +
				"the first key authorizes September and writes May"},
		{"duplicate keys, the last is the ALLOWED one",
			`{"VOUCHER_DATE":"2026-05-01","voucher_date":"2026-09-01","postings":[]}`, wantExactly,
			"2026-09-01",
			"the mirror image: the handler binds September, so refusing this body would refuse a " +
				"voucher the writer is going to date after the cutoff"},
		{"three duplicates, the last wins",
			`{"voucher_date":"2026-09-01","Voucher_Date":"2026-05-01","VOUCHER_DATE":"2026-09-02",` +
				`"postings":[]}`, wantExactly, "2026-09-02",
			"whatever rule the peek uses for duplicates must be encoding/json's rule, not a rule of " +
				"its own that happens to be stricter or looser"},

		// TRAILING BYTES — the door the round-4 enumeration named and NOBODY DROVE.
		// decodeJSON uses json.NewDecoder().Decode(), which takes the first JSON
		// value and ignores the rest; json.Unmarshal REJECTS trailing bytes. So the
		// old peek answered "not JSON at all" -> nil -> cutoff off, while the
		// handler decoded and posted the object perfectly.
		{"trailing junk after a complete object",
			`{"voucher_date":"2026-05-01","postings":[]} this is not json`, wantExactly, may1,
			"one appended byte switched the cutoff off while the handler posted the voucher normally"},
		{"a second JSON object after the first",
			`{"voucher_date":"2026-05-01","postings":[]}{"voucher_date":"2026-09-01"}`, wantExactly, may1,
			"the handler reads the FIRST value only, so the second object is decoration — the peek " +
				"must read exactly as far as the handler does"},
		{"trailing newline and junk",
			"{\"voucher_date\":\"2026-05-01\"}\n\n<<<", wantExactly, may1,
			"drainBody TRUNCATES at maxJSONBody, so an oversized body arrives here as a complete " +
				"first object followed by nothing — the same shape as this one"},
		{"leading whitespace", "  \t\n" + `{"voucher_date":"2026-05-01"}`, wantExactly, may1,
			"whitespace before the value is ordinary and must not be read as junk"},

		// ESCAPED FORMS. The key and the value are both UNQUOTED before matching, so
		// these ARE voucher_date and ARE 2026-05-01 to the handler. An exact byte
		// compare never sees the key the handler saw.
		{"the key is written with a unicode escape",
			`{"\u0076oucher_date":"2026-05-01","postings":[]}`, wantExactly, may1,
			"the key unquotes to voucher_date, binds, and dates the voucher"},
		{"the key is escaped AND shouted",
			`{"\u0056OUCHER_DATE":"2026-05-01","postings":[]}`, wantExactly, may1,
			"escape and fold compose, so enumerating spellings can never be complete"},
		{"the underscore itself is escaped",
			`{"voucher\u005fdate":"2026-05-01","postings":[]}`, wantExactly, may1,
			"any character of the key can be escaped; there is no prefix or suffix to match on"},
		{"the VALUE is written with unicode escapes",
			`{"voucher_date":"2026\u002d05\u002d01","postings":[]}`, wantExactly, may1,
			"the value unquotes to 2026-05-01 for BOTH layers, so this must be read as a date and " +
				"not refused as junk — a hand-rolled unquote would refuse an ordinary voucher"},

		// NESTED AT DEPTH, with a hostile key — round 2's door and round 5's door
		// composed, which is the shape nobody tests and everybody ships.
		{"a sync intent's payload with a SHOUTED key",
			syncCommitBodyKB(`{"VOUCHER_DATE":"2026-05-01","postings":[]}`), wantExactly, may1,
			"POST /sync/commit answered 200 COMMITTED for exactly this shape; syncIntentReq.Payload IS " +
				"a voucherReq, so the nested object binds by the same rule as the top-level one"},
		{"a sync intent's payload, escaped key, deeper still",
			syncCommitBodyKB(`{"\u0056oucher\u005fDate":"2026-05-01","postings":[]}`), wantExactly, may1,
			"an offline node can post into a filed quarter at will if either half of this is missed"},
		{"the sync ENVELOPE's own keys are shouted too",
			`{"INTENTS":[{"op":"CREATE","PAYLOAD":{"VOUCHER_DATE":"2026-05-01"}}]}`, wantExactly, may1,
			"the handler binds `intents` and `payload` case-insensitively as well, so the whole path " +
				"to the date can be respelled — the walk must not depend on any key name at all"},

		// A BATCH IS ONLY AS PERMITTED AS ITS MOST-REFUSED MEMBER (round 2), with
		// round 5's keys. sync.Commit commits its intents together and answers one
		// status for the request, so there is no partial authorization to express.
		{"a batch judged by its EARLIEST member, hostile keys throughout",
			syncCommitBodyKB(
				`{"Voucher_Date":"2026-08-20"}`,
				`{"VOUCHER_DATE":"2026-05-01"}`,
				`{"voucher_DATE":"2026-09-02"}`), wantExactly, may1,
			"one allowed intent must not carry a back-dated one in with it; the operator's outcome " +
				"list would look like ordinary catch-up traffic"},
		{"a batch whose back-dated member is FIRST",
			syncCommitBodyKB(`{"VOUCHER_DATE":"2026-05-01"}`, `{"voucher_date":"2026-08-20"}`),
			wantExactly, may1, "order must not matter — Go's map order is randomised"},
		{"a batch whose back-dated member is LAST",
			syncCommitBodyKB(`{"voucher_date":"2026-08-20"}`, `{"VOUCHER_DATE":"2026-05-01"}`),
			wantExactly, may1, "order must not matter"},

		// -------------------------------------------------- ROUND 1, PRESERVED
		// A date the PDP cannot READ is a date Postgres CAN read: ledger.Post writes
		// the string straight into a DATE column. Now with round 5's keys, because a
		// respelled key and a respelled date compose.
		{"a loose date under a shouted key", `{"VOUCHER_DATE":"2026-5-1","postings":[]}`, wantRefuse, "",
			"Postgres reads \"2026-5-1\" as 1 May 2026, so a nil here lands the voucher in the lock " +
				"correctly dated with no trace that a control was skipped"},
		{"RFC 3339 under a title-cased key", `{"Voucher_Date":"2026-05-01T00:00:00Z"}`, wantRefuse, "",
			"a DATE column truncates the time part and stores 1 May 2026"},
		{"ISO basic under an escaped key", `{"\u0076oucher_date":"20260501"}`, wantRefuse, "",
			"Postgres accepts the basic form too"},
		{"a padded date", `{"VOUCHER_DATE":" 2026-05-01 "}`, wantRefuse, "",
			"Postgres trims and stores 1 May 2026"},
		{"a day that does not exist", `{"VOUCHER_DATE":"2026-02-30"}`, wantRefuse, "",
			"unreadable is unreadable; the handler's 400 comes AFTER authorization"},
		{"junk", `{"Voucher_Date":"notadate"}`, wantRefuse, "", "unreadable must refuse"},
		{"a number where the date goes", `{"VOUCHER_DATE":20260501}`, wantRefuse, "",
			"present-but-not-a-string must stay distinguishable from absent; a string field in the " +
				"probe would collapse it to \"\" and hand the cutoff its nil"},
		{"an object where the date goes", `{"VOUCHER_DATE":{"y":2026}}`, wantRefuse, "",
			"same: authorization runs BEFORE the handler's 400"},
		{"an array where the date goes", `{"voucher_DATE":["2026-05-01"]}`, wantRefuse, "", "same"},

		// -------------------------------------------------- ROUND 2, PRESERVED
		{"ONE unreadable date refuses the WHOLE body",
			syncCommitBodyKB(`{"voucher_date":"2026-08-20"}`, `{"VOUCHER_DATE":"2026-5-1"}`),
			wantRefuse, "",
			"a batch commits together, so an unreadable member must refuse the request rather than " +
				"letting the readable ones speak for it"},
		{"an unreadable date nested under a hostile envelope key",
			`{"INTENTS":[{"PAYLOAD":{"VOUCHER_DATE":"31 May 2026"}}]}`, wantRefuse, "",
			"the two doors compose"},

		// ------------------------------------- ROUND 1, THE OPPOSITE DIRECTION
		// ⛔ THE HALF THAT IS USUALLY MISSING, AND THE ONE ROUND 1'S MUTATION PROOF
		// CAUGHT. This peek is the fall-through for EVERY write in the product.
		{"no date at all: a ledger create",
			`{"name":"HDFC Current A/c","group_name":"Bank Accounts"}`, wantNoDate, "",
			"failing closed here refuses every master create at any company that has set a cutoff, " +
				"which is an outage and not a control"},
		{"an empty string is how a client says NO DATE", `{"VOUCHER_DATE":"","postings":[]}`,
			wantNoDate, "",
			"the handler leaves the field \"\", ledger.Post writes nullIfEmpty and the row is dated " +
				"into no period at all — there is nothing to back-date"},
		{"an explicit null, under a hostile key", `{"Voucher_Date":null,"postings":[]}`, wantNoDate, "",
			"a pointer probe must read null as the handler reads it: no date"},
		{"an empty object", `{}`, wantNoDate, "", "nothing to back-date"},
		{"a sync intent that carries no date",
			`{"intents":[{"op":"CREATE","class":"Journal","payload":{"narration":"x","postings":[]}}]}`,
			wantNoDate, "", "offline catch-up of undated intents must keep working"},
		{"an item invoice: nested arrays and objects, dates nowhere",
			`{"postings":[{"ledger_id":"a","dr_cr":"D","amount":"1.0000"}],` +
				`"inventory":[{"item_id":"b","qty":"1","rate":"1.0000"}],"gst":{"place_of_supply":"29"}}`,
			wantNoDate, "", "the walk must not invent a date out of nesting"},
		{"a price list whose date-shaped field is NOT a voucher date",
			`{"effective_from":"2026-05-01","rows":[{"qty_from":"1","rate":"9.0000"}]}`, wantNoDate, "",
			"only the key the handler binds a voucher date from counts; a fuzzy match on \"date\" " +
				"would refuse price lists, statement imports and half the masters"},
		{"a key that merely CONTAINS the field name",
			`{"prev_voucher_date_note":"2026-05-01","x_voucher_date":"2026-05-01"}`, wantNoDate, "",
			"encoding/json binds the WHOLE key or nothing, so a substring match would be looser than " +
				"the handler — refusing writes the writer would never date"},
		{"a whole CSV inside a JSON string",
			`{"file_name":"apr.csv","content":"date,desc\n01-05-2026,rent\n","mapping":{"txn_date":"date"}}`,
			wantNoDate, "", "a bank statement import must keep working under a cutoff"},
		{"a body that is not JSON at all", `--boundary\r\nContent-Disposition: form-data`,
			wantNoDate, "", "a multipart bill upload carries no voucher date"},
		{"an empty body", ``, wantNoDate, "",
			"the handler answers 400 empty request body; the cutoff has nothing to say"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := peekVoucherDate([]byte(tc.body))
			switch tc.want {
			case wantNoDate:
				if got != nil {
					t.Fatalf("peekVoucherDate(%s) = %s, want nil.\nWHY: %s\nThis peek is the "+
						"fall-through for EVERY write in the product, so a spurious date here is a 403 "+
						"on writes that were never dated at all", tc.body, got.Format("2006-01-02"), tc.why)
				}
			case wantRefuse:
				if got == nil {
					t.Fatalf("peekVoucherDate(%s) = nil — and nil means THE BACK-DATED CUTOFF DOES NOT "+
						"APPLY (authz.cutoffDeny returns early on a nil date).\nWHY: %s", tc.body, tc.why)
				}
				if !got.Before(earliestPlausibleCutoffKB) {
					t.Fatalf("peekVoucherDate(%s) = %s, want a date before every settable cutoff (%s) — "+
						"an unreadable date must be REFUSED by cutoffDeny, and a date this late is "+
						"not.\nWHY: %s", tc.body, got.Format("2006-01-02"),
						earliestPlausibleCutoffKB.Format("2006-01-02"), tc.why)
				}
			case wantExactly:
				want, err := time.Parse("2006-01-02", tc.date)
				if err != nil {
					t.Fatalf("test bug: %q is not a date", tc.date)
				}
				if got == nil {
					t.Fatalf("peekVoucherDate(%s) = nil, want %s — nil means THE BACK-DATED CUTOFF DOES "+
						"NOT APPLY, so this body is authorized as if it named no period at all while "+
						"ledger.Post dates it %s.\nWHY: %s", tc.body, tc.date, tc.date, tc.why)
				}
				if !got.Equal(want) {
					t.Fatalf("peekVoucherDate(%s) = %s, want %s — the PDP must judge the date the LEDGER "+
						"will store.\nWHY: %s", tc.body, got.Format("2006-01-02"), tc.date, tc.why)
				}
			}

			// ⛔ AND NOW THE PART NO HAND-WRITTEN EXPECTATION CAN DO: ask
			// encoding/json what the HANDLER binds out of these same bytes, and
			// require the peek not to be more permissive. This is the assertion that
			// survives the next spelling nobody thought of.
			assertPeekIsNoLooserThanTheHandlerKB(t, tc.body, got)
		})
	}
}

// TestPeekVoucherDateDepthBoundFailsClosed is round 2's property, kept separate
// because it is the one case where the peek DELIBERATELY does not agree with the
// handler: encoding/json will happily decode nesting far deeper than the walk
// goes, so the walk must refuse rather than stop looking.
func TestPeekVoucherDateDepthBoundFailsClosed(t *testing.T) {
	deep := `{"VOUCHER_DATE":"2026-05-01"}`
	for i := 0; i < voucherDatePeekMaxDepth+10; i++ {
		deep = `{"a":` + deep + `}`
	}
	got := peekVoucherDate([]byte(deep))
	if got == nil {
		t.Fatalf("peekVoucherDate(a voucher_date nested %d deep) = nil — nil means the cutoff does not "+
			"apply, so the depth bound would itself be the bypass. \"I gave up reading this body\" must "+
			"never be spelled the same way as \"this body carries no date\": that conflation IS the "+
			"defect this peek exists to un-make (bound = %d)",
			voucherDatePeekMaxDepth+10, voucherDatePeekMaxDepth)
	}
	if !got.Before(earliestPlausibleCutoffKB) {
		t.Fatalf("peekVoucherDate(nested past the bound) = %s, want a date before every settable cutoff",
			got.Format("2006-01-02"))
	}
	// And the bound must be generous enough for the shapes the API really accepts:
	// a sync intent's voucher date sits at depth 3.
	if voucherDatePeekMaxDepth < 8 {
		t.Errorf("voucherDatePeekMaxDepth = %d, which is close to the depth real bodies use (a sync "+
			"intent's date is at depth 3) — a bound this tight turns ordinary traffic into 403s",
			voucherDatePeekMaxDepth)
	}
}

// assertPeekIsNoLooserThanTheHandlerKB decodes `body` into the REAL wire structs
// with the REAL decode call and fails if the peek is more permissive than what
// encoding/json bound.
//
// ⛔ THIS IS THE WHOLE DEFECT CLASS, MECHANISED. Rounds 1–4 each asserted a list
// of spellings. The bypass each time was a spelling not on the list — so the
// assertion that matters is relative, not absolute: whatever the handler binds,
// the peek must present the PDP with a date no LATER (no more permissive) than
// that, and must never present nil when the handler bound a date. The peek is
// allowed to be STRICTER (it walks every object in the body, so it may find a
// date the route's own struct would ignore, and it takes the EARLIEST of them).
func assertPeekIsNoLooserThanTheHandlerKB(t *testing.T, body string, peeked *time.Time) {
	t.Helper()

	// decodeJSON is json.NewDecoder(body).Decode(v). Reproduced exactly — using
	// json.Unmarshal here instead would reintroduce the trailing-bytes divergence
	// INTO THE TEST and quietly excuse the bug.
	var flat voucherReq
	_ = json.NewDecoder(bytes.NewReader([]byte(body))).Decode(&flat)
	var nested syncCommitReq
	_ = json.NewDecoder(bytes.NewReader([]byte(body))).Decode(&nested)

	bound := []string{}
	if flat.VoucherDate != "" {
		bound = append(bound, flat.VoucherDate)
	}
	for _, in := range nested.Intents {
		if in.Payload.VoucherDate != "" {
			bound = append(bound, in.Payload.VoucherDate)
		}
	}
	if len(bound) == 0 {
		return // the handler binds no date out of this body; nothing to compare.
	}

	if peeked == nil {
		t.Fatalf("DIVERGENCE: encoding/json binds voucher_date=%s out of %s into the real wire struct — "+
			"so ledger.Post will date the row that way — while peekVoucherDate returned nil, and nil "+
			"means THE BACK-DATED CUTOFF DOES NOT APPLY. The PDP authorized a body it read differently "+
			"from the writer. That divergence, in whatever spelling, IS this defect: it has been case "+
			"(round 5), depth (round 4), the date's own format (round 3) and the decode call itself "+
			"(trailing bytes) — fix the peek, not this test", strings.Join(bound, ", "), body)
	}
	for _, spelled := range bound {
		d, err := time.Parse("2006-01-02", spelled)
		if err != nil {
			// The handler bound something the PDP cannot read, and Postgres may well
			// read it (ledger.Post hands the string to a DATE column unparsed). The
			// only safe answer is a refusal.
			if !peeked.Before(earliestPlausibleCutoffKB) {
				t.Fatalf("DIVERGENCE: encoding/json binds voucher_date=%q out of %s — a spelling "+
					"time.Parse rejects and Postgres accepts — while peekVoucherDate returned %s, "+
					"which no cutoff will deny. A date the PDP cannot read must REFUSE",
					spelled, body, peeked.Format("2006-01-02"))
			}
			continue
		}
		if peeked.After(d) {
			t.Fatalf("DIVERGENCE: encoding/json binds voucher_date=%s out of %s, but peekVoucherDate "+
				"answered %s — LATER, i.e. more permissive. The cutoff would allow a write the ledger "+
				"then dates inside the locked period. The peek may be stricter than the handler; it may "+
				"never be looser", spelled, body, peeked.Format("2006-01-02"))
		}
	}
}

// syncCommitBodyKB builds a real /sync/commit body whose intents carry the given
// RAW JSON payload objects. The payloads are spliced as raw JSON so a test can
// send hostile keys and wrong-typed values without making the envelope invalid —
// an invalid envelope would make peekVoucherDate answer nil for the ordinary
// "not JSON" reason and the test would pass for the wrong reason.
func syncCommitBodyKB(payloads ...string) string {
	var b strings.Builder
	b.WriteString(`{"intents":[`)
	for i, p := range payloads {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"idempotency_key":"kb%d","client_voucher_id":%q,"op":"CREATE",`+
			`"class":"Journal","hlc":{"wall_millis":1,"counter":0,"node_id":"n1"},"node_id":"n1",`+
			`"base_version":0,"payload":%s}`, i, uuid.NewString(), p)
	}
	b.WriteString(`]}`)
	return b.String()
}

// ---------------------------------------------------------------------------
// 3. THE SIBLING PEEK. Fixing one copy of a divergence and leaving the other is
//    how this control has already been re-opened four times.
// ---------------------------------------------------------------------------

// TestPeekDepositDateIsDecodedTheWayTheHandlerDecodesIt covers the door round 5
// left open ONE ROUTE SIDEWAYS.
//
// peekVoucherDate was changed to decode with json.NewDecoder().Decode() because
// that is what decodeJSON does and json.Unmarshal's rejection of trailing bytes
// was itself a bypass. peekDepositDate — the challan's date, the OTHER route the
// cutoff was made to reach — still used json.Unmarshal. Measured before the fix:
//
//	peekDepositDate(`{"deposit_date":"2026-05-01"}`)          -> 2026-05-01
//	peekDepositDate(`{"deposit_date":"2026-05-01"} trailing`) -> nil (cutoff OFF)
//
// And unlike a loose spelling, this date is one masters.validateTDSChallan
// ACCEPTS, so the challan is recorded inside the locked quarter.
func TestPeekDepositDateIsDecodedTheWayTheHandlerDecodesIt(t *testing.T) {
	may1 := time.Date(2026, time.May, 1, 0, 0, 0, 0, time.UTC)

	t.Run("trailing bytes must not switch the cutoff off", func(t *testing.T) {
		for _, body := range []string{
			`{"deposit_date":"2026-05-01","bsr_code":"0004329"} trailing junk`,
			`{"deposit_date":"2026-05-01"}{"deposit_date":"2026-09-01"}`,
			"{\"deposit_date\":\"2026-05-01\"}\n\n<<<",
		} {
			got := peekDepositDate([]byte(body))
			if got == nil {
				t.Errorf("peekDepositDate(%s) = nil — nil means the back-dated cutoff DOES NOT APPLY, "+
					"while handleTDSChallan's decodeJSON reads the first JSON value and IGNORES the "+
					"trailing bytes, so the deposit is recorded dated 2026-05-01 inside a locked and "+
					"filed quarter. The register and the filed return then stop agreeing, and the "+
					"discrepancy surfaces at the portal against the department's .csi file", body)
				continue
			}
			if !got.Equal(may1) {
				t.Errorf("peekDepositDate(%s) = %s, want 2026-05-01 — the PDP must judge the date the "+
					"STORE will write", body, got.Format("2006-01-02"))
			}
		}
	})

	t.Run("the case of the key follows encoding/json, as the handler's does", func(t *testing.T) {
		for _, body := range []string{
			`{"DEPOSIT_DATE":"2026-05-01"}`,
			`{"Deposit_Date":"2026-05-01"}`,
			`{"deposit_date":"2026-05-01"}`,
		} {
			got := peekDepositDate([]byte(body))
			if got == nil || !got.Equal(may1) {
				t.Errorf("peekDepositDate(%s) = %v, want 2026-05-01 — tdsChallanReq.DepositDate binds "+
					"this key case-insensitively, so the deposit is dated 2026-05-01 and the cutoff must "+
					"see it", body, got)
			}
		}
	})

	t.Run("an ordinary body is unaffected", func(t *testing.T) {
		// ⛔ THE CONTROL. The decode change must not have cost the ordinary challan
		// its date, and must not have turned "no deposit date" into a refusal — the
		// store's own 400 is the better answer for that body (see peekDepositDate).
		if got := peekDepositDate([]byte(`{"deposit_date":"2026-06-07"}`)); got == nil ||
			!got.Equal(time.Date(2026, time.June, 7, 0, 0, 0, 0, time.UTC)) {
			t.Errorf("peekDepositDate(an ordinary challan) = %v, want 2026-06-07", got)
		}
		for _, body := range []string{`{}`, `{"bsr_code":"0004329"}`, `not json`, ``,
			`{"deposit_date":""}`, `{"deposit_date":null}`} {
			if got := peekDepositDate([]byte(body)); got != nil {
				t.Errorf("peekDepositDate(%s) = %s, want nil — a challan with no readable deposit date "+
					"dies at 400 in validateTDSChallan with a sentence about the date, which is a more "+
					"useful answer than a 403 about a period lock", body, got.Format("2006-01-02"))
			}
		}
	})
}

// ---------------------------------------------------------------------------
// 4. THE DRIVE. The unit tests above cannot see a handler/PDP divergence,
//    because they only ever ask ONE of the two layers to run.
// ---------------------------------------------------------------------------

// TestBackDatedCutoffRefusesARespelledVoucherDateKeyOverHTTP is the gate for the
// re-driven defect, through the real router, the real seeded Owner level, a real
// cutoff set through the real admin API, and RAW request bodies — because
// json.Marshal of a Go map cannot produce a duplicate key, a shouted key or a
// trailing byte, which are the three shapes that were driven.
//
// The legs, and why each is load-bearing:
//
//	CONTROL  strict voucher_date 2026-05-01           -> 403
//	         Proves the cutoff is live on THIS company; without it every 403
//	         below could be any other denial and the test proves nothing.
//	ATTACK   the same date under a respelled key      -> MUST 403
//	NOTHING  GET /day-book over the locked month      -> no probe may appear
//	         ⛔ A 403 THAT STILL WROTE IS THIS REPO'S SIGNATURE FAILURE. The
//	         status code alone is not the assertion.
//	ALLOWED  a post-cutoff date, respelled key        -> MUST 201, and land
//	         ⛔ THE HALF THAT IS USUALLY MISSING. A peek that refused anything
//	         unusual would pass every leg above and stop the business trading.
func TestBackDatedCutoffRefusesARespelledVoucherDateKeyOverHTTP(t *testing.T) {
	s := payrollServer(t)
	f := newSpellingFixture(t, s)

	levelID := aclOwnerLevelID(t, s, f.base, f.ownerTok)
	const cutoff = "2026-07-01"
	aclSetCutoff(t, s, f.base, levelID, f.ownerTok, cutoff)

	// journalRaw builds a balanced journal as RAW JSON with the date written under
	// `dateKey` — which may be shouted, escaped, or repeated.
	//
	// ⚠️ IT CARRIES AN EXPLICIT voucher_no, for auth_backdated_spelling_test.go's
	// reason: with voucher_no empty, ledger.Post takes the auto-numbering branch
	// and calls finYearOf on the date, so a body that got PAST the PDP could still
	// come back non-201 for a reason that has nothing to do with authorization —
	// and the test would read that as "the cutoff held".
	journalRaw := func(dateFields, tag string) string {
		return `{` + dateFields + `,"voucher_no":"KB-` + tag + `","narration":"keybinding probe ` + tag +
			`","created_by":"keybinding-test","postings":[` +
			`{"ledger_id":"` + f.expense + `","dr_cr":"D","amount":"1000.0000"},` +
			`{"ledger_id":"` + f.bank + `","dr_cr":"C","amount":"1000.0000"}]}`
	}

	// ------------------------------------------------------------------ CONTROL
	rec, body := aclRequestRawKB(t, s, http.MethodPost, f.base+"/vouchers/journal", f.ownerTok,
		journalRaw(`"voucher_date":"2026-05-01"`, "CONTROL"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("CONTROL: POST /vouchers/journal voucher_date=2026-05-01 under a %s cutoff = %d, want "+
			"403 — the cutoff is not live on this company, so every assertion below proves nothing (%s)",
			cutoff, rec.Code, rec.Body.String())
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, cutoff) {
		t.Fatalf("CONTROL refusal reason = %q, want it to name the cutoff %s — this test distinguishes "+
			"the cutoff's 403 from every other 403 by that sentence (%s)", msg, cutoff, rec.Body.String())
	}

	// ------------------------------------------------------------------- ATTACK
	// ⛔ THE FIRST THREE WERE DRIVEN AND RETURNED 201 WITH voucher_no 6, 7 AND 8.
	attacks := []struct {
		tag        string
		dateFields string
		why        string
	}{
		{"SHOUT", `"VOUCHER_DATE":"2026-05-01"`,
			"driven on the wire: 201, voucher_no 6, and it is in the operator's May 2026 Day Book"},
		{"TITLE", `"Voucher_Date":"2026-05-01"`,
			"driven on the wire: 201, voucher_no 7 — one of these was driven from a real browser " +
				"session with a real passkey step-up"},
		{"INNER", `"voucher_DATE":"2026-05-01"`, "driven on the wire: 201, voucher_no 8"},
		{"ESCAPED", `"\u0056OUCHER_DATE":"2026-05-01"`,
			"the key is unquoted BEFORE it is matched, so an exact byte compare never sees the key " +
				"the handler saw"},
		{"DUPLICATE", `"voucher_date":"2026-09-01","VOUCHER_DATE":"2026-05-01"`,
			"encoding/json assigns each matching key in turn, so the LAST wins and the handler stores " +
				"MAY while a peek reading the first authorizes SEPTEMBER"},
		{"ESCAPEDVALUE", `"\u0056oucher\u005fDATE":"2026\u002d05\u002d01"`,
			"key and value escapes compose; the value unquotes to 2026-05-01 for the writer"},
	}
	for _, a := range attacks {
		raw := journalRaw(a.dateFields, a.tag)
		rec, _ := aclRequestRawKB(t, s, http.MethodPost, f.base+"/vouchers/journal", f.ownerTok, raw)
		if rec.Code != http.StatusForbidden {
			t.Errorf("ATTACK %s: POST /vouchers/journal %s under a %s cutoff = %d, want 403 — the "+
				"back-dated write was ALLOWED because the PDP peek and encoding/json disagreed about "+
				"which key carries the date. %s\nbody sent: %s\nresponse: %s",
				a.tag, a.dateFields, cutoff, rec.Code, a.why, raw, rec.Body.String())
		}
	}
	// TRAILING BYTES — the same body the CONTROL leg was refused for, plus junk.
	// decodeJSON stops at the end of the first JSON value; json.Unmarshal does not.
	trailing := journalRaw(`"voucher_date":"2026-05-01"`, "TRAILING") + " not json any more"
	if rec, _ := aclRequestRawKB(t, s, http.MethodPost, f.base+"/vouchers/journal", f.ownerTok,
		trailing); rec.Code != http.StatusForbidden {
		t.Errorf("ATTACK TRAILING: the CONTROL body plus one appended junk byte = %d, want 403 — "+
			"decodeJSON uses json.NewDecoder().Decode(), which reads the FIRST JSON value and ignores "+
			"the rest, so the handler posts this voucher normally. A peek that used json.Unmarshal read "+
			"it as \"not JSON\" and answered nil, and nil means the cutoff does not apply (%s)",
			rec.Code, rec.Body.String())
	}
	// NESTED + RESPELLED: round 4's door and round 5's door composed.
	syncRaw := `{"intents":[{"idempotency_key":"kb-sync-` + uuid.NewString()[:8] +
		`","client_voucher_id":"` + uuid.NewString() + `","op":"CREATE","class":"Journal",` +
		`"hlc":{"wall_millis":1,"counter":0,"node_id":"kb"},"node_id":"kb","base_version":0,` +
		`"payload":{"VOUCHER_DATE":"2026-05-01","narration":"keybinding probe SYNC",` +
		`"created_by":"keybinding-test","postings":[` +
		`{"ledger_id":"` + f.expense + `","dr_cr":"D","amount":"1000.0000"},` +
		`{"ledger_id":"` + f.bank + `","dr_cr":"C","amount":"1000.0000"}]}}]}`
	if rec, _ := aclRequestRawKB(t, s, http.MethodPost, f.base+"/sync/commit", f.ownerTok,
		syncRaw); rec.Code != http.StatusForbidden {
		t.Errorf("ATTACK SYNC: POST /sync/commit with intents[0].payload.VOUCHER_DATE=2026-05-01 under "+
			"a %s cutoff = %d, want 403 — this exact shape answered 200 COMMITTED on the wire. "+
			"syncIntentReq.Payload IS a voucherReq, so the nested object binds the key by the same "+
			"case-insensitive rule as the top-level one (%s)", cutoff, rec.Code, rec.Body.String())
	}

	// ------------------------------------------------------------------ NOTHING
	// ⛔ THE ASSERTION THAT IS NOT A STATUS CODE. Every refusal above must have
	// written NOTHING, read back through the report an operator would actually look
	// at.
	if landed := keyBindingProbesInDayBook(t, s, f, "2026-01-01", "2026-06-30"); len(landed) > 0 {
		t.Errorf("A REFUSED WRITE STILL LANDED: the Day Book for the locked period contains %v — the "+
			"requests answered 403 and the vouchers are in the books inside a quarter whose returns are "+
			"filed. That is worse than a plain bypass, because the audit trail says they were refused",
			landed)
	}
	// And the DUPLICATE-key attack must not have landed a SEPTEMBER voucher either:
	// the peek must not have authorized the first key's date and let the handler
	// write the second key's.
	if landed := keyBindingProbesInDayBook(t, s, f, "2026-09-01", "2026-09-30"); len(landed) > 0 {
		t.Errorf("the DUPLICATE-key attack wrote a voucher dated in September: %v — a refused request "+
			"must write nothing at all", landed)
	}

	// ------------------------------------------------------------------ ALLOWED
	// ⛔ THE CONTROL, AND IT IS THE WHOLE REASON THIS CANNOT BE FIXED BY REFUSING
	// ANYTHING ODD. A post-cutoff date must still post — including under a
	// respelled key, because encoding/json binds it and the operator's client is
	// entitled to send it.
	for _, ok := range []struct{ tag, dateFields, wantDate string }{
		{"ALLOWED-PLAIN", `"voucher_date":"2026-08-20"`, "2026-08-20"},
		{"ALLOWED-SHOUT", `"VOUCHER_DATE":"2026-08-21"`, "2026-08-21"},
		{"ALLOWED-DUP", `"voucher_date":"2026-05-01","voucher_date":"2026-08-22"`, "2026-08-22"},
	} {
		rec, body := aclRequestRawKB(t, s, http.MethodPost, f.base+"/vouchers/journal", f.ownerTok,
			journalRaw(ok.dateFields, ok.tag))
		if rec.Code != http.StatusCreated {
			t.Fatalf("%s: POST /vouchers/journal %s under a %s cutoff = %d, want 201 — a date AFTER the "+
				"cutoff must still post. Refusing it means the fix is a blanket refusal and nobody can "+
				"enter today's vouchers (%s)", ok.tag, ok.dateFields, cutoff, rec.Code, rec.Body.String())
		}
		vid, _ := body["voucher_id"].(string)
		if vid == "" {
			t.Fatalf("%s: 201 with no voucher_id (%s)", ok.tag, rec.Body.String())
		}
		if got := aclVoucherDate(t, s, f.base, vid, f.ownerTok); got != ok.wantDate {
			t.Errorf("%s: the allowed voucher landed dated %q, want %s — it posted, but not on the date "+
				"that was authorized, which means the PDP and the writer read the body differently in "+
				"the direction that happens to be harmless today", ok.tag, got, ok.wantDate)
		}
	}
	// The three allowed probes, and ONLY those three, are in the August Day Book.
	if landed := keyBindingProbesInDayBook(t, s, f, "2026-08-01", "2026-08-31"); len(landed) != 3 {
		t.Errorf("the August Day Book shows %v, want exactly the three allowed probes — a 201 that "+
			"wrote nothing is the other half of this repo's signature failure", landed)
	}
}

// TestBackDatedCutoffRefusesAChallanBodyWithTrailingBytes drives the sibling
// peek's door: the challan route, whose date is `deposit_date`, was still peeked
// with json.Unmarshal after peekVoucherDate had been moved off it.
//
//	CONTROL  deposit_date 2026-05-31, strict body   -> 403 (the cutoff is live)
//	ATTACK   the same body plus a trailing byte     -> MUST 403, and record nothing
//	ALLOWED  a post-cutoff deposit, trailing byte   -> MUST 201
//	         ⛔ the control: matching decodeJSON must not make a trailing-byte
//	         body a refusal in its own right.
func TestBackDatedCutoffRefusesAChallanBodyWithTrailingBytes(t *testing.T) {
	s := payrollServer(t)
	f := newTDSChallanFixture(t, s, "kbtrail")

	levelID := aclOwnerLevelID(t, s, f.base, f.ownerTok)
	// After the fixture's deduction (2026-05-10) and before its ordinary deposit
	// date (2026-06-07), so both directions below are meaningful.
	const cutoff = "2026-06-01"
	aclSetCutoff(t, s, f.base, levelID, f.ownerTok, cutoff)

	rawChallan := func(depositDate, challanNo, suffix string) string {
		b := f.challanBody()
		b["deposit_date"] = depositDate
		b["challan_no"] = challanNo
		raw, err := json.Marshal(b)
		if err != nil {
			t.Fatalf("marshal challan: %v", err)
		}
		return string(raw) + suffix
	}

	// ------------------------------------------------------------------ CONTROL
	if rec, body := aclRequestRawKB(t, s, http.MethodPost, f.base+"/tds-challans", f.ownerTok,
		rawChallan("2026-05-31", "05131", "")); rec.Code != http.StatusForbidden {
		t.Fatalf("CONTROL: POST /tds-challans deposit_date=2026-05-31 under a %s cutoff = %d, want 403 "+
			"— the cutoff is not reaching this route at all, so the attack leg proves nothing (%s)",
			cutoff, rec.Code, rec.Body.String())
	} else if msg, _ := body["error"].(string); !strings.Contains(msg, cutoff) {
		t.Fatalf("CONTROL refusal = %q, want it to name the cutoff %s (%s)", msg, cutoff,
			rec.Body.String())
	}

	// ------------------------------------------------------------------- ATTACK
	if rec, _ := aclRequestRawKB(t, s, http.MethodPost, f.base+"/tds-challans", f.ownerTok,
		rawChallan("2026-05-31", "05132", " trailing")); rec.Code != http.StatusForbidden {
		t.Errorf("ATTACK: the CONTROL challan plus one appended junk byte = %d, want 403 — "+
			"handleTDSChallan's decodeJSON reads the first JSON value and IGNORES the trailing bytes, "+
			"so the deposit is recorded dated 2026-05-31 inside a locked and filed quarter, while a peek "+
			"using json.Unmarshal reads the body as \"not JSON\", returns nil, and nil means the "+
			"back-dated cutoff does not apply. A challan inserted into a filed quarter makes the "+
			"register disagree with the return, and the discrepancy surfaces at the portal against the "+
			"department's own .csi file (%s)", rec.Code, rec.Body.String())
	}

	// ------------------------------------------------------------------ NOTHING
	rec, body := aclRequest(t, s, http.MethodGet, f.base+"/tds-challans?tax_year_start=2026",
		f.ownerTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /tds-challans = %d, want 200 — without the read-back this test can only assert "+
			"status codes, and a 403 that still recorded the deposit would pass (%s)", rec.Code,
			rec.Body.String())
	}
	challans, _ := body["challans"].([]any)
	for _, raw := range challans {
		c, _ := raw.(map[string]any)
		if d, _ := c["deposit_date"].(string); strings.HasPrefix(d, "2026-05") {
			t.Errorf("a REFUSED challan is recorded, dated %s — inside the locked period. The request "+
				"answered 403 and the deposit is in the register (%s)", d, rec.Body.String())
		}
	}

	// ------------------------------------------------------------------ ALLOWED
	// ⛔ THE CONTROL. Trailing bytes are not themselves a reason to refuse: the
	// handler accepts them, so the PDP must judge the DATE and nothing else.
	if rec, _ := aclRequestRawKB(t, s, http.MethodPost, f.base+"/tds-challans", f.ownerTok,
		rawChallan("2026-06-07", "05122", " trailing")); rec.Code != http.StatusCreated {
		t.Fatalf("ALLOWED: POST /tds-challans deposit_date=2026-06-07 (after the %s cutoff) with a "+
			"trailing byte = %d, want 201 — matching decodeJSON must not turn a body the handler "+
			"accepts into a refusal of its own; that would break every client that pads its bodies (%s)",
			cutoff, rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Helpers.
// ---------------------------------------------------------------------------

// aclRequestRawKB is aclRequest with a RAW body string.
//
// ⛔ IT EXISTS BECAUSE json.Marshal CANNOT EXPRESS THE ATTACKS. A Go map cannot
// hold two spellings of the same key, cannot preserve the document order that
// decides which duplicate wins, cannot write a \u-escaped key, and cannot append
// a trailing byte. A test that built its bodies with json.Marshal could not have
// driven any of the shapes that were actually driven.
func aclRequestRawKB(t *testing.T, s *Server, method, path, token, raw string) (
	*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)

	var decoded map[string]any
	if rec.Body.Len() > 0 && rec.Header().Get("Content-Type") == "application/json" {
		if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("%s %s: decode response %q: %v", method, path, rec.Body.String(), err)
		}
	}
	return rec, decoded
}

// keyBindingProbesInDayBook returns this test's probes that are in the Day Book
// for [from, to], read through the ordinary report route — so what it reports is
// what an operator would see on the Day Book screen.
func keyBindingProbesInDayBook(t *testing.T, s *Server, f spellingFixture, from, to string) []string {
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
		if strings.HasPrefix(n, "keybinding probe ") {
			d, _ := e["date"].(string)
			found = append(found, n+" dated "+d)
		}
	}
	return found
}
