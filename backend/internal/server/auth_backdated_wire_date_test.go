package server

// auth_backdated_wire_date_test.go — ⛔ THE STRUCTURAL GATE FOR "A DATE UNDER A
// DIFFERENT KEY", WHICH IS NOW THE THIRD TIME THIS CLASS HAS SHIPPED OPEN.
//
// ===========================================================================
// THE CLASS, AND WHY A LIST OF ROUTES WAS NEVER GOING TO CLOSE IT
// ===========================================================================
// The back-dated cutoff judges a write on the date the write CARRIES, and
// voucherDateFromRequest has to find that date in the body. Three times a route
// has carried its date under a key the peek did not look for, and each time the
// control was not weak — it was ENTIRELY ABSENT, because cutoffDeny skips when
// the date is nil:
//
//	POST /vouchers/payroll   `wage_month`     the most sensitive voucher in the
//	                                          product, with no cutoff at all
//	POST /tds-challans       `deposit_date`   ⛔ shipped with THREE comments
//	                                          asserting the control was in place
//	PUT  /bank-reconciliation/…  `bank_date`  ⛔ shipped with a comment asserting
//	                                          the control was in place, and was
//	                                          found by an audit that DROVE the
//	                                          product rather than read it
//
// §7.2 of the parity audit is the lesson from the six doors of the same control:
// each round fixed the door it was SHOWN, asserted a list of hostile inputs, and
// the next bypass was always off the list. What finally worked was asserting a
// PROPERTY. Two exist already — assertPeekIsNoLooserThanTheHandlerKB (the peek
// may never be looser than the handler) and
// TestEveryWriteRouteNamingAnExistingDocumentIsAccountedFor (a write route naming
// a stored document cannot be mounted in silence).
//
// ⛔ NEITHER OF THEM CAN SEE THIS CLASS. The first only looks at bodies it is
// handed; the second walks ROUTES, and the pay run, the challan and the bank date
// were never invisible as routes — they were invisible as FIELDS. So this file
// walks the fields:
//
//	EVERY struct field in package server whose json key looks like a date must
//	be classified, either as one the cutoff JUDGES (and the classification is
//	then EXERCISED against the real voucherDateFromRequest, not asserted) or as
//	one it cannot own, WITH the reason.
//
// A new `settlement_date` on a new write body therefore fails HERE, naming
// itself, and whoever adds it has to decide which of the two it is. That is the
// only kind of gate that has ever held in this repo.

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// wireDateVerdict is one classification. Exactly one of judged / reason is set.
type wireDateVerdict struct {
	// judged: the cutoff reads this field. method/route/body/want then EXERCISE
	// that claim through the real voucherDateFromRequest — because a claim in a
	// map is a comment, and comments are what shipped three times.
	judged bool
	method string
	route  string
	body   string
	want   string

	// reason: why the cutoff cannot own this date. Required when !judged.
	reason string
}

// wireDatesInPackageServer classifies every date-shaped json key on this
// package's wire types. ⛔ THE KEY IS "TYPE.json_key", NOT A ROUTE: the whole
// point is that the three defects were invisible at route granularity.
var wireDatesInPackageServer = map[string]wireDateVerdict{
	// ---- JUDGED. Each is exercised below. ---------------------------------
	"voucherReq.voucher_date": {
		judged: true, method: http.MethodPost,
		route: "/v1/companies/{companyID}/vouchers/journal",
		body:  `{"voucher_date":"2026-05-01","postings":[]}`, want: "2026-05-01",
	},
	"payRunReq.wage_month": {
		judged: true, method: http.MethodPost,
		route: "/v1/companies/{companyID}/vouchers/payroll",
		// ⚠️ The pay run's date is DERIVED — the last day of the wage month, by
		// payrolldb.LastDayOf, the same call the posting path makes. Adding a
		// voucher_date field instead would let the two drift invisibly.
		body: `{"wage_month":"2026-05"}`, want: "2026-05-31",
	},
	"tdsChallanReq.deposit_date": {
		judged: true, method: http.MethodPost,
		route: "/v1/companies/{companyID}/tds-challans",
		body:  `{"deposit_date":"2026-05-01"}`, want: "2026-05-01",
	},
	"setBankDateReq.bank_date": {
		judged: true, method: http.MethodPut,
		route: "/v1/companies/{companyID}/bank-reconciliation/{ledgerID}/{voucherID}/{legNo}",
		body:  `{"bank_date":"2026-05-01"}`, want: "2026-05-01",
	},

	// ---- NOT A DATE THE CUTOFF CAN OWN, WITH THE REASON. ------------------
	"voucherDateProbe.voucher_date": {reason: "the PEEK's own probe type, not a wire contract"},

	"billAllocationReq.due_date": {reason: "" +
		"a bill's MATURITY, not the date of the entry: the voucher carrying it is dated by " +
		"voucher_date and judged on that. A due date places nothing in a period"},
	"openingBillReq.due_date": {reason: "" +
		"the maturity of one opening bill. ⚠️ Restating a ledger's OPENING balance does move a " +
		"balance filed periods depend on — but it is a masters write with no date on the wire and " +
		"no voucher, so it is a different control's problem (the same verdict, verbatim, that " +
		"writeRoutesNamingADocumentButNotMovingIt records for PUT /ledgers/{ledgerID}/opening)"},

	"orderReq.order_date": {reason: "" +
		"an order posts NOTHING (internal/orders is pinned by archtest to the db layer only), so no " +
		"figure of any period moves. routeAction leaves the cutoff deliberately inert on orders in " +
		"BOTH directions, so there is no asymmetry to exploit"},
	"orderLineReq.due_date": {reason: "a promised delivery date on an order, which posts nothing"},

	"monthInputReq.first_pf_member_on": {reason: "" +
		"nested INSIDE payRunReq: one employee's PF-membership fact, not the date of the run. The " +
		"pay run's own date is wage_month and it is judged"},

	"companyReq.fy_start_date": {reason: "" +
		"POST /companies CREATES the company. There is no company yet, therefore no security level " +
		"and no cutoff — the PDP is not even reached with a company scope"},
	"levelReq.backdated_cutoff": {reason: "" +
		"⛔ this IS the cutoff, not a date judged by it. The admin surface that sets the lock; " +
		"whether an owner may move it is the ACL's question, not the cutoff's"},

	"columnMappingReq.date_layout": {reason: "a Go time LAYOUT for parsing the bank's CSV, not a date"},
	"columnMappingReq.value_date":  {reason: "the NAME of a column in the operator's CSV, not a date"},
	"columnMappingReq.txn_date":    {reason: "the NAME of a column in the operator's CSV, not a date"},

	"stockBatchReq.mfg_date": {reason: "" +
		"a property of the GOODS (a batch master), not of an entry in a period. Nothing it changes " +
		"appears in a filed return"},
	"stockBatchReq.expiry_date": {reason: "a property of the goods; see stockBatchReq.mfg_date"},

	"tcsDeclarationReq.declaration_date": {reason: "" +
		"a s.394(2) declaration is a MASTER keyed on (buyer, section, tax year) — ledger/tcs.go " +
		"reads it by that triple and never by date, and it cannot restate a posted voucher (the " +
		"collection is stored on the voucher). Any sale it exempts is a FUTURE voucher, which is " +
		"itself judged by the cutoff. Checked, not assumed"},
	"tcsDeclarationReq.received_date": {reason: "" +
		"s.394(3)'s clock starts here, but the document is still the master above"},

	// ---- RESPONSES. Nothing decides a write. ------------------------------
	"allocatableDeductionView.deduction_date": {reason: "response"},
	"bankReconLineView.bank_date":             {reason: "response"},
	"bankReconLineView.voucher_date":          {reason: "response"},
	"companyViewResp.fy_start_date":           {reason: "response"},
	"levelJSON.backdated_cutoff":              {reason: "response"},
	"stockBatchListItem.mfg_date":             {reason: "response"},
	"stockBatchListItem.expiry_date":          {reason: "response"},
	"tcsDeclarationView.declaration_date":     {reason: "response"},
	"tcsDeclarationView.received_date":        {reason: "response"},
	"tcsDeclarationView.forward_due_date":     {reason: "response (DERIVED — the s.394(3) deadline)"},
	"tdsChallanView.deposit_date":             {reason: "response"},
}

// looksLikeADate is deliberately WIDER than "contains date". `wage_month` is the
// field that made the cutoff inert on payroll and it contains no such word, so a
// matcher built on "date" alone would have missed the very defect this file is
// named for.
func looksLikeADate(jsonKey string) bool {
	k := strings.ToLower(jsonKey)
	return strings.Contains(k, "date") || strings.Contains(k, "month") || strings.HasSuffix(k, "_on")
}

// dateShapedWireFields parses THIS package's non-test sources and returns every
// "Type.json_key" whose key looks like a date. Source-level rather than
// reflection-based on purpose: reflect can only see types a test already names,
// which is the same blind spot that let three of these ship.
func dateShapedWireFields(t *testing.T) map[string]string {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse package server: %v", err)
	}
	out := map[string]string{}
	for _, p := range pkgs {
		for fileName, f := range p.Files {
			ast.Inspect(f, func(n ast.Node) bool {
				ts, ok := n.(*ast.TypeSpec)
				if !ok {
					return true
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					return true
				}
				for _, fld := range st.Fields.List {
					if fld.Tag == nil {
						continue
					}
					tag, uerr := strconv.Unquote(fld.Tag.Value)
					if uerr != nil {
						continue
					}
					key := strings.Split(reflect.StructTag(tag).Get("json"), ",")[0]
					if key == "" || key == "-" || !looksLikeADate(key) {
						continue
					}
					out[ts.Name.Name+"."+key] = fileName
				}
				return true
			})
		}
	}
	if len(out) < 20 {
		t.Fatalf("found only %d date-shaped wire fields; the parse or the matcher is broken and this "+
			"gate is no longer looking at anything", len(out))
	}
	return out
}

// TestEveryDateShapedWireFieldIsAccountedForByTheCutoff is the gate.
func TestEveryDateShapedWireFieldIsAccountedForByTheCutoff(t *testing.T) {
	found := dateShapedWireFields(t)

	for name, file := range found {
		v, classified := wireDatesInPackageServer[name]
		if !classified {
			t.Errorf("%s (%s) carries a DATE on the wire and nothing says whether the back-dated "+
				"cutoff judges it.\n"+
				"This is the shape that has now shipped OPEN three times — the pay run's wage_month, "+
				"the challan's deposit_date and the bank reconciliation's bank_date — and each time "+
				"the control was not weak but ABSENT, because cutoffDeny skips entirely when the PDP "+
				"has no date. Two of the three shipped with a COMMENT asserting the control was in "+
				"place.\n"+
				"Decide which this is and record it in wireDatesInPackageServer: either the cutoff "+
				"judges it (add an arm to voucherDateFromRequest and mark it judged, which is then "+
				"EXERCISED here), or state the reason it cannot own this date.", name, file)
			continue
		}
		if !v.judged && strings.TrimSpace(v.reason) == "" {
			t.Errorf("%s is classified as not-judged with an EMPTY reason — the reason is the whole "+
				"value of the entry", name)
		}
	}

	// A stale entry is as bad as a missing one: it reads as coverage of a field
	// that no longer exists, and the renamed one is then unclassified in silence.
	for name := range wireDatesInPackageServer {
		if _, ok := found[name]; !ok {
			t.Errorf("wireDatesInPackageServer classifies %q, which no longer exists in package "+
				"server. A renamed field is an UNCLASSIFIED field; move the entry rather than "+
				"deleting the classification", name)
		}
	}
}

// TestTheCutoffActuallyReadsEveryDateItClaimsToRead exercises the `judged`
// entries against the REAL voucherDateFromRequest.
//
// ⛔ THIS IS THE HALF THAT MAKES THE MAP ABOVE WORTH ANYTHING. Marking a field
// "judged" is exactly the kind of claim the challan and the bank date shipped
// with — written down, believed, and false. Here the claim is executed: the body
// goes through the same function the middleware calls, on a request that carries
// the route's own chi params, and the date must come back.
func TestTheCutoffActuallyReadsEveryDateItClaimsToRead(t *testing.T) {
	names := make([]string, 0, len(wireDatesInPackageServer))
	for name, v := range wireDatesInPackageServer {
		if v.judged {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) < 4 {
		t.Fatalf("only %d fields are marked judged; the four the cutoff reads are voucher_date, "+
			"wage_month, deposit_date and bank_date", len(names))
	}

	companyID := uuid.NewString()
	for _, name := range names {
		v := wireDatesInPackageServer[name]
		t.Run(name, func(t *testing.T) {
			path, rctx := synthPath(v.route, companyID)
			req := httptest.NewRequest(v.method, path, strings.NewReader(v.body))
			req = req.WithContext(withRouteCtx(req, rctx))

			// The action comes from routeAction rather than being asserted, so a
			// route that stops being a write also fails here instead of quietly
			// making the date check unreachable.
			act, _, _, mapped := routeAction(req)
			if !mapped {
				t.Fatalf("%s %s is not mapped by routeAction", v.method, v.route)
			}
			if !act.IsWrite() {
				t.Fatalf("%s %s maps to %v, which is not a write — the cutoff never runs on it, so "+
					"marking %s judged is false", v.method, v.route, act, name)
			}

			got := voucherDateFromRequest(req, act)
			if got == nil {
				t.Fatalf("voucherDateFromRequest for %s = nil. ⛔ nil means THE CUTOFF DOES NOT "+
					"APPLY: cutoffDeny returns early, and a write dated into a filed period is "+
					"accepted. Body was %s", name, v.body)
			}
			want, err := time.Parse("2006-01-02", v.want)
			if err != nil {
				t.Fatalf("bad want in the registry: %v", err)
			}
			if !got.Equal(want) {
				t.Errorf("voucherDateFromRequest for %s = %s, want %s — the PDP would judge a "+
					"different day from the one the handler stores",
					name, got.Format("2006-01-02"), v.want)
			}
		})
	}
}

// withRouteCtx is the one piece of plumbing these need: routeSuffix reads
// {companyID} out of chi's route context, so a synthesised request must carry it
// or the matcher never sees the route the middleware would see.
func withRouteCtx(r *http.Request, rctx *chi.Context) context.Context {
	return context.WithValue(r.Context(), chi.RouteCtxKey, rctx)
}
