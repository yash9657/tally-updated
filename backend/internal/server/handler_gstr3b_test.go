package server

// handler_gstr3b_test.go is the HANDLER-level cover for GET
// /v1/companies/{companyID}/gstr3b. It is in-package (package server, not
// server_test) because the handler method is unexported, exactly like
// handler_itc_test.go, whose newITCLiveServer / scoped / callErr helpers it
// reuses.
//
// WHAT THESE TESTS EXIST FOR. Everything here is about the `disclosures` field,
// which is the only thing on this endpoint that tells a filer the return they
// are about to sign is NOT filing-ready. Three specific ways it can lie:
//
//  1. A disclosure the reconciliation raised never reaches the response. This
//     really happened: the handler intersected the forwarded list with
//     gstr.KnownDisclosures and threw away anything unknown, which silently ate
//     `sec-16-6-revocation-not-modelled` on every statement-backed 3B.
//     TestGSTR3BForwardsEverySection16_6AndEveryOtherReconciliationDisclosure is
//     the guard, and it is written as a SUPERSET check over whatever the
//     reconciliation actually produced, so it catches the next dropped token too
//     and not just this one.
//  2. The no-statement path quietly gains or loses a token, so a frontend built
//     against the documented set stops recognising one of them.
//     TestGSTR3BWithoutStatementIDCarriesTheWholeDisclosureSet pins the set.
//  3. A malformed `as_on` is ignored rather than refused on the path the file
//     header used to call "unchanged".
//     TestGSTR3BRefusesAMalformedAsOnEvenWithoutAStatement pins the 400.
//
// The DB-backed tests here do NOT skip when Postgres is down — see
// newITCLiveServer's own comment. A skipped disclosure pin is indistinguishable
// from a passing one.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/compliance"
	"github.com/khata/cored/internal/compliance/gstr"
	"github.com/khata/cored/internal/compliance/itc"
	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/masters"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/platform/testdb"
)

// The January-2026 window every test below files for, and a reconciliation
// instant well past that period's deemed-acceptance cutoff and well inside the
// s.16(4) window for a FY2025-26 document.
const (
	g3bFrom      = "2026-01-01"
	g3bTo        = "2026-01-31"
	g3bPeriod    = "012026"
	g3bAsOn      = "2026-03-01T10:00:00+05:30"
	g3bSupplier  = "29ABCDE1234F1Z5"
	g3bState     = "29"
	g3bInvNo     = "INV-3B-1"
	g3bInvDate   = "2026-01-15"
	g3bTaxable   = "10000"
	g3bHalfTax   = "900.0000"
	g3bFilerGSTN = "29ZZZZZ0000Z1Z5"
)

// g3bFixture creates a company, its purchase-side masters and ONE intra-state
// 18% purchase carrying the supplier's own document identity, and returns a live
// Server scoped to it.
//
// The purchase is not decoration. `itc-eligibility-not-classified` is raised
// only when a purchase actually reaches table 4 without a decision, so a company
// with no books at all would let the disclosure-set pin below pass while
// asserting six tokens instead of seven — the exact under-count this file exists
// to stop.
func g3bFixture(t *testing.T) (*Server, context.Context, uuid.UUID) {
	t.Helper()
	srv := newITCLiveServer(t)

	companyID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new company id: %v", err)
	}
	ctx := db.WithCompany(context.Background(), companyID)
	if _, err := srv.identity.CreateCompany(ctx, identity.Company{
		ID:          companyID,
		Name:        "GSTR3B Co " + companyID.String()[:8],
		FYStartDate: "2025-04-01",
	}); err != nil {
		t.Fatalf("create company: %v", err)
	}
	testdb.Cleanup(t, companyID)

	vtID, err := srv.masters.VoucherTypeID(ctx, "Purchase")
	if err != nil {
		t.Fatalf("purchase voucher type: %v", err)
	}
	ensure := func(name, group string) uuid.UUID {
		t.Helper()
		id, err := srv.masters.EnsureLedger(ctx, name, group, "tester")
		if err != nil {
			t.Fatalf("ensure ledger %s: %v", name, err)
		}
		return id
	}
	purchases := ensure("Local Purchases", "Purchase Accounts")
	inCGST := ensure("Input CGST", "Duties & Taxes")
	inSGST := ensure("Input SGST", "Duties & Taxes")
	inIGST := ensure("Input IGST", "Duties & Taxes")

	supplier, err := srv.masters.CreateLedger(ctx, mastersSupplier(), "tester")
	if err != nil {
		t.Fatalf("supplier ledger: %v", err)
	}

	tx := decimal.RequireFromString(g3bTaxable)
	half := decimal.RequireFromString(g3bHalfTax)
	if _, err := srv.ledger.PostVoucher(ctx, ledger.VoucherInput{
		Class:         ledger.Purchase,
		VoucherTypeID: vtID,
		PartyLedgerID: supplier,
		VoucherNo:     "PU-3B-1",
		VoucherDate:   g3bInvDate,
		Party:         "Karnataka Supplies Pvt Ltd",
		CreatedBy:     "tester",
		Postings: []ledger.PostingInput{
			{LedgerID: purchases, DrCr: ledger.Debit, Amount: tx},
			{LedgerID: inCGST, DrCr: ledger.Debit, Amount: half},
			{LedgerID: inSGST, DrCr: ledger.Debit, Amount: half},
			{LedgerID: supplier, DrCr: ledger.Credit, Amount: tx.Add(half).Add(half)},
		},
		GST: &ledger.GSTParams{
			SupplierStateCode:      g3bState,
			PlaceOfSupplyStateCode: g3bState,
			Lines: []compliance.Line{
				{TaxableValue: tx, GSTRatePct: decimal.RequireFromString("18"), HSN: "8471"},
			},
			CGSTLedgerID: inCGST,
			SGSTLedgerID: inSGST,
			IGSTLedgerID: inIGST,
		},
		Inward: &ledger.InwardDocument{
			SupplierInvoiceNo:   g3bInvNo,
			SupplierInvoiceDate: g3bInvDate,
			ITCEligibility:      "ELIGIBLE",
		},
	}); err != nil {
		t.Fatalf("post purchase: %v", err)
	}
	return srv, ctx, companyID
}

// mastersSupplier is split out only so g3bFixture stays readable; the GSTIN is
// what makes the purchase comparable against the statement at all.
func mastersSupplier() masters.Ledger {
	return masters.Ledger{
		Name: "Karnataka Supplies Pvt Ltd", GroupName: "Sundry Creditors",
		GSTIN: g3bSupplier, StateCode: g3bState,
		RegistrationType: masters.GSTRegular,
	}
}

// g3bIngest2B uploads a minimal January-2026 GSTR-2B carrying the one invoice
// the fixture posted, and returns the statement id.
func g3bIngest2B(t *testing.T, srv *Server, ctx context.Context) uuid.UUID {
	t.Helper()
	body := map[string]any{
		"data": map[string]any{
			"gstin":  g3bFilerGSTN,
			"rtnprd": g3bPeriod,
			"gendt":  "14-02-2026",
			"docdata": map[string]any{
				"b2b": []any{map[string]any{
					"ctin":     g3bSupplier,
					"trdnm":    "Karnataka Supplies Pvt Ltd",
					"supprd":   g3bPeriod,
					"supfildt": "11-02-2026",
					"inv": []any{map[string]any{
						"inum": g3bInvNo, "dt": "15-01-2026", "pos": g3bState,
						"rev": "N", "itcavl": "Y",
						"txval": g3bTaxable, "cgst": "900.00", "sgst": "900.00",
						"igst": "0.00", "cess": "0.00",
					}},
				}},
			},
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal 2B: %v", err)
	}
	res, err := srv.itc.Ingest(ctx, itc.IngestInput{
		Kind:       itc.KindGSTR2B,
		Raw:        raw,
		IngestedAt: g3bAsOnTime(t),
		IngestedBy: "tester",
	})
	if err != nil {
		t.Fatalf("ingest 2B: %v", err)
	}
	if len(res.Skipped) != 0 {
		t.Fatalf("the fixture 2B was only partially parsed (%v); it would raise "+
			"statement-partially-parsed and stop being a clean fixture", res.Skipped)
	}
	return res.StatementID
}

func g3bAsOnTime(t *testing.T) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, g3bAsOn)
	if err != nil {
		t.Fatalf("parse as_on: %v", err)
	}
	return v
}

// g3bDisclosures drives the handler and returns the status plus the response's
// `disclosures` field. It accepts either response shape — the bare return, or
// the {"gstr3b":..., "reconciliation_ok":...} envelope.
func g3bDisclosures(t *testing.T, srv *Server, ctx context.Context, companyID uuid.UUID, query string) (int, []string, string) {
	t.Helper()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("companyID", companyID.String())
	req := httptest.NewRequest(http.MethodGet,
		"/v1/companies/"+companyID.String()+"/gstr3b"+query, nil).
		WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	srv.handleGSTR3B(rec, req)

	body := strings.TrimRight(rec.Body.String(), "\n")
	if rec.Code != http.StatusOK {
		return rec.Code, nil, body
	}
	var envelope struct {
		Wrapped *struct {
			Disclosures []string `json:"disclosures"`
		} `json:"gstr3b"`
		Disclosures []string `json:"disclosures"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("decode 3B response %q: %v", body, err)
	}
	if envelope.Wrapped != nil {
		return rec.Code, envelope.Wrapped.Disclosures, body
	}
	return rec.Code, envelope.Disclosures, body
}

// ---------------------------------------------------------------------------
// THE s.16(6) FORWARDING GUARD
// ---------------------------------------------------------------------------

// TestGSTR3BForwardsEverySection16_6AndEveryOtherReconciliationDisclosure is the
// pin on the one defect that made a real return lie.
//
// compliance/itc discloses `sec-16-6-revocation-not-modelled` UNCONDITIONALLY:
// credit for a period during which a registration stood cancelled and was later
// restored is not computed, because no table in this schema holds the
// cancellation or revocation dates it turns on. The handler used to intersect
// the reconciliation's disclosures with gstr's closed vocabulary before
// forwarding them, and that token was not in the vocabulary, so it was dropped —
// on the GSTR-3B, which is the document the filer signs.
//
// The assertion is deliberately a SUPERSET check against what the reconciliation
// actually emitted for this very request, not a hard-coded list. A hard-coded
// list would pin today's token and let tomorrow's be eaten by the same
// mechanism.
func TestGSTR3BForwardsEverySection16_6AndEveryOtherReconciliationDisclosure(t *testing.T) {
	srv, ctx, companyID := g3bFixture(t)
	statementID := g3bIngest2B(t, srv, ctx)

	// What the reconciliation itself says about this period, for this instant.
	rctx := itc.WithAsOn(ctx, g3bAsOnTime(t))
	rep, err := srv.itc.Reconcile(rctx, g3bFrom, g3bTo, itc.Options{AsOn: g3bAsOnTime(t)})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !containsStr(rep.Disclosures, itc.DiscSec16_6NotModelled) {
		t.Fatalf("the reconciliation no longer discloses %q at all (%v). s.16(6) is still not "+
			"computed, so this test has stopped testing anything and the gap has gone silent "+
			"one layer earlier", itc.DiscSec16_6NotModelled, rep.Disclosures)
	}

	code, got, body := g3bDisclosures(t, srv, ctx, companyID,
		// as_on carries a "+05:30" offset: it MUST be percent-encoded, or the
		// query parser reads the plus as a space and the handler 400s on an
		// instant the test meant to be valid.
		"?from="+g3bFrom+"&to="+g3bTo+"&statement_id="+statementID.String()+
			"&as_on="+url.QueryEscape(g3bAsOn)+"&gstin="+g3bFilerGSTN)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", code, body)
	}

	if !containsStr(got, gstr.DiscSec16_6NotModelled) {
		t.Fatalf("the GSTR-3B response does not disclose %q. Its disclosures are %v.\n"+
			"THE CONSEQUENCE: s.16(6) credit — for a period during which this registration "+
			"stood cancelled and was later restored — has NOT been computed, and the return "+
			"the filer signs does not say so. The error is an UNDER-claim, so nothing about "+
			"the return looks wrong: the filer simply pays tax they do not owe, and has no "+
			"way to know they must check those periods by hand. Do not re-introduce a filter "+
			"between itc.Report.Disclosures and gstr.Options3B.ITCPolicy.SourceDisclosures.",
			gstr.DiscSec16_6NotModelled, got)
	}

	// EVERY token the reconciliation raised must survive the trip.
	for _, want := range rep.Disclosures {
		if !containsStr(got, want) {
			t.Errorf("the reconciliation disclosed %q and the GSTR-3B response did not (%v). "+
				"A reason this return is not filing-ready reached the reconciliation report and "+
				"then vanished before the document the filer signs; a frontend switching on the "+
				"response's disclosures can never show it", want, got)
		}
	}
	if !containsStr(got, gstr.DiscNotFilingReady) {
		t.Errorf("disclosures %v do not contain %q: a statement-backed return is still not "+
			"filing-ready, and without this flag a frontend has nothing to refuse on",
			got, gstr.DiscNotFilingReady)
	}
}

// ---------------------------------------------------------------------------
// THE NO-STATEMENT DISCLOSURE SET
// ---------------------------------------------------------------------------

// TestGSTR3BWithoutStatementIDCarriesTheWholeDisclosureSet pins the SEVEN tokens
// the default path emits, as a set.
//
// The file header of handler_gstr3b.go is the contract a frontend author reads
// before deciding which tokens to render, and it used to name TWO
// (`no-inward-statement` and `not-filing-ready`). A UI built on that recognises
// two and silently drops five, including `itc-eligibility-not-classified` — the
// token that says every posted input rupee entered table 4(A) with NO
// ELIGIBILITY CHECK AT ALL. This test is what stops the comment and the code
// drifting apart again: change either and it fails.
//
// Byte-identity of the rest of the document is pinned separately, by
// TestGSTR3BWithoutStatementIDIsTheBareReturn in handler_itc_test.go (handler
// level) and TestBuild3B_NilITCSourceIsByteIdentical in package gstr (build3B
// level).
func TestGSTR3BWithoutStatementIDCarriesTheWholeDisclosureSet(t *testing.T) {
	srv, ctx, companyID := g3bFixture(t)

	code, got, body := g3bDisclosures(t, srv, ctx, companyID,
		"?from="+g3bFrom+"&to="+g3bTo+"&gstin="+g3bFilerGSTN)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", code, body)
	}

	want := []string{
		gstr.Disc4D1ReclaimNotModelled,
		gstr.DiscCessNotModelled,
		gstr.DiscITCEligibilityNotClassified,
		gstr.DiscNoInwardStatement,
		gstr.DiscNotFilingReady,
		gstr.DiscPortalSchemaUnverified,
		gstr.DiscRule4243NotComputed,
	}
	gotSorted := append([]string(nil), got...)
	sort.Strings(gotSorted)
	if strings.Join(gotSorted, ",") != strings.Join(want, ",") {
		t.Fatalf("the no-statement_id disclosure set changed.\n got: %v\nwant: %v\n"+
			"This set IS the contract in the handler_gstr3b.go file header, and a frontend "+
			"renders exactly the tokens it was told to expect. A token added here and not "+
			"there is a reason the return is not filing-ready that no user will ever see; a "+
			"token removed here is a UI branch that has gone dead. Update both together.",
			gotSorted, want)
	}
	if !containsStr(got, gstr.DiscITCEligibilityNotClassified) {
		t.Errorf("%q is missing. Every posted input rupee was reported available in table 4(A) "+
			"with no s.16(4), s.17(5) or supplier-filing check whatsoever, and the return does "+
			"not admit it", gstr.DiscITCEligibilityNotClassified)
	}
}

// ---------------------------------------------------------------------------
// as_on IS PARSED EVEN WITHOUT A STATEMENT
// ---------------------------------------------------------------------------

// TestGSTR3BRefusesAMalformedAsOnEvenWithoutAStatement pins the one thing about
// the no-statement_id path that is NOT "unchanged".
//
// as_on is parsed unconditionally, before the statement_id branch, so an
// unparseable instant is a 400 even though nothing on this path would have used
// it. That is the right trade — accepting a malformed instant and quietly
// answering for a different one is a wrong number nobody can see — but the file
// header called the path "unchanged, byte for byte", so the behaviour needs a
// test of its own rather than a reader's assumption.
//
// No database: the refusal happens before anything touches the pool, and
// noDBServer's nil TxRunner turns a regression that let it through into a panic
// rather than a pass.
func TestGSTR3BRefusesAMalformedAsOnEvenWithoutAStatement(t *testing.T) {
	srv := noDBServer()
	code, msg := callErr(t, srv.handleGSTR3B,
		scoped(http.MethodGet, "/v1/companies/x/gstr3b?from=2026-01-01&to=2026-01-31&as_on=yesterday", ""))
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: an as_on the server could not read must be refused, not "+
			"replaced with now — the instant decides which IMS records have deem-accepted, so "+
			"silently substituting one answers for a period the caller never asked about", code)
	}
	if !strings.Contains(msg, "as_on") {
		t.Errorf("error %q must name as_on so the caller can fix the parameter", msg)
	}
}

func containsStr(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
