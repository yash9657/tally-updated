package server_test

// einvoice_http_test.go — CAN A CLIENT ACTUALLY RAISE AN e-INVOICE, AND READ THE
// IRN BACK OFF THE VOUCHER?
//
// ===========================================================================
// ⛔⛔ WHAT IS AND IS NOT STUBBED HERE, STATED UP FRONT
// ===========================================================================
// Nothing that matters. The whole re-entrant call graph is real and runs over
// real sockets:
//
//	the test  --(in-process HTTP)-->  cored's real router, real PDP, real RLS
//	cored     --(real socket)------>  statutory.EInvoiceAPI, the REAL handler
//	statutoryd--(real socket)------>  cored's real GET /vouchers/{id}
//	statutoryd--(real socket)------>  cored's real POST .../external-reference
//
// The IRN workflow is the real GenerateIRNWorkflow over the real Postgres
// PGStore (migration 0013's statutory_step, not the in-memory one), the freeze
// hook really writes voucher_external_reference, and statutoryd really
// authenticates to cored with a svc: token minted from cored's own /auth/token.
//
// Two things are not production objects, and neither is load-bearing:
//   - THE PROVIDER is statutory.FakeProvider, which is what the shipped
//     statutoryd binary uses too. The alternative is calling the National
//     Informatics Centre.
//   - statutoryd is an httptest server rather than a `go run ./cmd/statutoryd`
//     child, but it mounts the SAME statutory.EInvoiceAPI that cmd/statutoryd
//     mounts, at the same route constant. That is exactly why the handler was
//     lifted out of package main: a hand-rolled look-alike would agree with
//     whatever cored sends by construction and would prove nothing.
//
// ⛔ NO SQL IN THE FIXTURE REACHES AROUND THE API. Every voucher, ledger, item
// and movement is created over HTTP (newEWBFixture, shared with T5's test). The
// only direct reads are the godown id, which has no create route, and the
// statutory_step cleanup.
//
// ===========================================================================
// ⛔⛔ THE POOL IS CAPPED AT ONE CONNECTION, AND THAT IS THE RE-ENTRANCY PROOF
// ===========================================================================
// HANDOFF §1.4 B.2's design risk: the call graph is cored -> statutoryd ->
// cored, so a proxy handler that holds a database transaction across the call is
// holding a connection the CALLBACK then needs. The stated failure mode is "a
// hung request under concurrency rather than an error in a test", which is
// exactly the kind of defect a comfortable pool hides.
//
// So this file gives cored a pool of ONE connection. The correct
// implementation — read the books, COMMIT, then call out — never needs two at
// once and passes. An implementation that wrapped the statutoryd call in
// RunInTx cannot get a connection for the callback and BLOCKS, which the client
// deadline turns into a 503 and this file turns into a failure.
//
// ⚠️ That cap makes this file's server DIFFERENT from newServer(t)'s, which is
// why it builds its own.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/server"
	"github.com/khata/cored/internal/statutory"
	"github.com/khata/cored/migrations"
)

// einvStack is the whole three-party stack, wired.
type einvStack struct {
	srv  *server.Server
	ewb  ewbFixture // the company, party, item and stock, created over HTTP
	sent *sentRequests

	// stopStatutoryd takes the IRN plane down mid-test, so the "statutoryd is
	// not running" refusal can be driven without a second company.
	stopStatutoryd func()
}

// sentRequests records the raw JSON body cored POSTed to statutoryd.
//
// ⛔ IT IS A TEE AND NOT A STUB. The body is read, recorded, and handed on to
// the real handler unchanged, so recording it changes nothing about what the
// workflow sees. It exists because the LINE-ITEM BUILDER is the deliverable of
// this whole track and its output is otherwise invisible: statutoryd answers
// with an IRN, which is an opaque digest, so a builder that emitted the wrong
// HSN, the wrong quantity or no lines at all would produce a perfectly
// plausible-looking 201.
type sentRequests struct {
	mu   sync.Mutex
	last []byte
}

func (s *sentRequests) record(b []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.last = append([]byte(nil), b...)
}

// nothingSent reports whether cored made NO call to statutoryd at all.
//
// ⛔ IT EXISTS FOR THE REFUSAL TESTS, and it is the assertion that separates
// "cored declined to build the payload" from "cored built a bad payload and
// statutoryd caught it". Only the first is safe: statutoryd's freeze hook pins
// the IRN irreversibly, so a refusal that still reaches it is worse than the
// document it refused.
func (s *sentRequests) nothingSent() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.last) == 0
}

func (s *sentRequests) lastRequest(t *testing.T) map[string]any {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.last) == 0 {
		t.Fatalf("cored never POSTed anything to statutoryd")
	}
	var out map[string]any
	if err := json.Unmarshal(s.last, &out); err != nil {
		t.Fatalf("cored sent statutoryd a body that is not JSON (%q): %v", s.last, err)
	}
	return out
}

// newEInvoiceStack brings up statutoryd, then cored, then points each at the
// other.
//
// ⚠️ THE ORDER IS FORCED AND IS NOT ARBITRARY. cored reads STATUTORYD_BASE_URL
// at construction, and statutoryd needs cored's URL and a token minted BY cored.
// The cycle is broken by starting statutoryd with an unset cored URL and filling
// it in afterwards — HTTPCoredClient.BaseURL and .ServiceToken are plain fields,
// which is exactly how `make run-statutoryd` fills them from the environment.
func newEInvoiceStack(t *testing.T) *einvStack {
	t.Helper()
	if testing.Short() {
		t.Skip("the e-invoice stack needs Postgres and two HTTP servers")
	}
	// ⛔ CORED_SKIP_DB WAS NOT HONOURED HERE, and this is the one test in the
	// package that APPLIES MIGRATIONS without checking it — so a "hermetic"
	// `CORED_SKIP_DB=1 go test ./...` still migrated the database behind
	// adminDSN. Measured in the W3 lane on 2026-09-28: 0096/0097 landed in the
	// shared `tally` database from a CORED_SKIP_DB=1 run and were dropped by
	// hand.
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("CORED_SKIP_DB is set")
	}
	ctx := context.Background()

	adminConn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Skipf("admin postgres unavailable: %v", err)
	}
	if err := migrations.Apply(ctx, adminConn); err != nil {
		_ = adminConn.Close(ctx)
		t.Fatalf("apply migrations: %v", err)
	}
	_ = adminConn.Close(ctx)

	// ---- statutoryd, over the REAL durable StepStore. The in-memory store would
	// have worked and would have proved less: the durable one is what the shipped
	// daemon uses, and it is the thing that makes an idempotent re-post return the
	// same IRN rather than mint a second.
	stepPool, err := pgxpool.New(ctx, appDSN)
	if err != nil {
		t.Fatalf("statutoryd step-store pool: %v", err)
	}
	t.Cleanup(stepPool.Close)

	coredClient := statutory.NewHTTPCoredClientWithToken("", "")
	sent := &sentRequests{}
	statRouter := chi.NewRouter()
	statRouter.Use(teeBody(sent))
	(&statutory.EInvoiceAPI{
		Store:    statutory.NewPGStore(stepPool),
		Provider: statutory.FakeProvider{},
		Cored:    coredClient,
	}).Mount(statRouter)
	statSrv := httptest.NewServer(statRouter)
	stopOnce := sync.Once{}
	stop := func() { stopOnce.Do(statSrv.Close) }
	t.Cleanup(stop)

	// ---- cored, pointed at it, with a ONE-CONNECTION pool (see the header).
	t.Setenv("AUTH_DEV_TOKEN_ENABLED", "true")
	t.Setenv("STATUTORYD_BASE_URL", statSrv.URL)
	// A short deadline so the mutation this file exists to catch FAILS FAST
	// instead of hanging the suite. The correct implementation never waits.
	t.Setenv("STATUTORYD_TIMEOUT", "20s")

	srv, err := server.New(ctx, appDSN+"&pool_max_conns=1")
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	t.Cleanup(srv.Close)

	coredSrv := httptest.NewServer(srv.Router())
	t.Cleanup(coredSrv.Close)

	// ---- close the loop. The subject MUST be exactly svc:statutoryd: cored's
	// auth middleware has a "svc:" fast path built for this caller — no app_user
	// row, no membership, no security level, no step-up — and without it the
	// freeze hook is a 401 in a company statutoryd is not a member of.
	coredClient.BaseURL = coredSrv.URL
	coredClient.ServiceToken = mintToken(t, srv, "svc:statutoryd")

	f := newEWBFixture(t, srv)

	// statutory_step is keyed on a workflow id string and has no company column,
	// so testdb.Cleanup cannot reach it. Sweep this company's workflows by hand.
	companyID := strings.TrimPrefix(f.base, "/v1/companies/")
	t.Cleanup(func() {
		_, _ = stepPool.Exec(context.Background(),
			`DELETE FROM statutory_step WHERE workflow_id LIKE $1`, "irn:"+companyID+":%")
	})

	return &einvStack{srv: srv, ewb: f, sent: sent, stopStatutoryd: stop}
}

// teeBody records the request body and hands an identical one to the next
// handler.
func teeBody(sink *sentRequests) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			_ = r.Body.Close()
			if err == nil {
				sink.record(body)
			}
			r.Body = io.NopCloser(strings.NewReader(string(body)))
			next.ServeHTTP(w, r)
		})
	}
}

// raiseEInvoice POSTs the raise for one voucher.
func (s *einvStack) raiseEInvoice(t *testing.T, voucherID string) (int, map[string]any) {
	t.Helper()
	rec, body := doRaw(t, s.srv, http.MethodPost, s.ewb.base+"/e-invoices", s.ewb.ownerTok,
		map[string]any{"voucher_id": voucherID})
	return rec.Code, body
}

// TestAClientCanRaiseAnEInvoiceOverHTTPAndReadItBack is the reachability pin.
// The assertion spans wire -> ledger -> line-item builder -> statutoryd -> the
// durable workflow -> the freeze hook -> back into cored -> wire.
func TestAClientCanRaiseAnEInvoiceOverHTTPAndReadItBack(t *testing.T) {
	st := newEInvoiceStack(t)
	voucherID := postInvoice(t, st.srv, st.ewb, "200.0000")

	// ---- CONTROL: nothing has been raised yet, so the GET is a 404. Without it
	// every assertion below is consistent with the route answering 200
	// unconditionally.
	crec, _ := doRaw(t, st.srv, http.MethodGet, st.ewb.base+"/e-invoices/"+voucherID, st.ewb.ownerTok, nil)
	if crec.Code != http.StatusNotFound {
		t.Fatalf("CONTROL: GET before any e-invoice is raised = %d, want 404 (%s)",
			crec.Code, crec.Body.String())
	}

	status, body := st.raiseEInvoice(t, voucherID)
	if status != http.StatusCreated {
		t.Fatalf("POST /e-invoices = %d, want 201 (%v)", status, body)
	}

	irn, _ := body["irn"].(string)
	if len(irn) != 64 {
		t.Fatalf("irn = %q, want a 64-char reference (%v)", irn, body)
	}
	if got, _ := body["status"].(string); got != "ACT" {
		t.Errorf("status = %q, want ACT", got)
	}
	if got, _ := body["ack_no"].(string); got == "" {
		t.Errorf("ack_no is empty. Nothing persists it, so the raise response is the ONLY " +
			"place it will ever appear")
	}
	pinned, _ := body["pinned_voucher_no"].(string)
	if pinned == "" {
		t.Fatalf("pinned_voucher_no is empty; the freeze hook did not report a pinned number (%v)", body)
	}
	if vno, _ := body["voucher_no"].(string); vno != pinned {
		t.Errorf("the response says voucher_no=%q but pinned_voucher_no=%q. They are the same "+
			"number and a screen showing both would be showing a contradiction", vno, pinned)
	}

	// =====================================================================
	// ⛔ THE LINE-ITEM BUILDER — THE DELIVERABLE OF THIS TRACK. Assert what
	// cored actually SENT, because an IRN is a digest and would look fine
	// whatever the payload said.
	// =====================================================================
	req := st.sent.lastRequest(t)

	// The identity cored derived from the `company` row and the party `ledger`
	// row. A browser supplying these by hand is the design this replaces.
	if got, _ := req["seller_gstin"].(string); got != "29AACCD1234K1Z8" {
		t.Errorf("seller_gstin = %q, want the COMPANY's own 29AACCD1234K1Z8", got)
	}
	if got, _ := req["buyer_gstin"].(string); got != "29AACCB4321K1Z2" {
		t.Errorf("buyer_gstin = %q, want the PARTY LEDGER's 29AACCB4321K1Z2", got)
	}
	if got, _ := req["seller_legal_name"].(string); got == "" {
		t.Errorf("seller_legal_name is empty; NIC rejects a payload without one")
	}
	if got, _ := req["buyer_legal_name"].(string); got != "Acme Buyer" {
		t.Errorf("buyer_legal_name = %q, want the party ledger's name", got)
	}
	// ⛔ THE BUYER'S STATE CODE IS THE VOUCHER'S PLACE OF SUPPLY, not the party
	// master's state_code. The two agree in this fixture; the assertion is here
	// so the FIELD is pinned even where the values coincide.
	if got, _ := req["buyer_state_code"].(string); got != "29" {
		t.Errorf("buyer_state_code = %q, want 29 (the voucher's place of supply)", got)
	}

	// The item list — the thing statutoryd cannot derive and a browser could not
	// know (HANDOFF §1.4 B.1 item 1).
	lines, _ := req["lines"].([]any)
	if len(lines) != 1 {
		t.Fatalf("cored sent %d line(s), want 1: %v", len(lines), req["lines"])
	}
	line, _ := lines[0].(map[string]any)
	if got, _ := line["HSN"].(string); got != "7216" {
		t.Errorf("the line's HSN = %q, want 7216 — the code the ITEM MASTER carries. A wrong "+
			"HSN is an e-invoice describing different goods", got)
	}
	if got, _ := line["Desc"].(string); !strings.HasPrefix(got, "MS Angle") {
		t.Errorf("the line's Desc = %q, want the stock item's name", got)
	}
	if got, _ := line["GstRatePct"].(string); got != "18" {
		t.Errorf("the line's GstRatePct = %q, want 18", got)
	}
	// ⛔ MONEY IS RECONCILED TO THE PAISA, NOT WITHIN A TOLERANCE. 200 units at
	// 500 is 100,000 taxable; 18% intra-State is 9,000 + 9,000 and no IGST.
	for field, want := range map[string]string{
		"Taxable":   "100000.00",
		"CGST":      "9000.00",
		"SGST":      "9000.00",
		"IGST":      "0.00",
		"UnitPrice": "500.00",
		"LineTotal": "118000.00",
	} {
		if got, _ := line[field].(string); got != want {
			t.Errorf("the line's %s = %q, want %q", field, got, want)
		}
	}
	if got, _ := line["Qty"].(string); !strings.HasPrefix(got, "200") {
		t.Errorf("the line's Qty = %q, want 200", got)
	}

	// The invoice totals, and the FOOTING NIC enforces: total == taxable + taxes.
	for field, want := range map[string]string{
		"taxable":       "100000.00",
		"cgst":          "9000.00",
		"sgst":          "9000.00",
		"igst":          "0.00",
		"invoice_total": "118000.00",
	} {
		if got, _ := req[field].(string); got != want {
			t.Errorf("the request's %s = %q, want %q", field, got, want)
		}
	}

	// =====================================================================
	// ⛔ AND NOW READ IT BACK OUT THROUGH THE OUTERMOST SURFACE.
	// =====================================================================
	grec, gbody := doRaw(t, st.srv, http.MethodGet, st.ewb.base+"/e-invoices/"+voucherID, st.ewb.ownerTok, nil)
	if grec.Code != http.StatusOK {
		t.Fatalf("GET /e-invoices/%s = %d, want 200 (%s)", voucherID, grec.Code, grec.Body.String())
	}
	if got, _ := gbody["irn"].(string); got != irn {
		t.Errorf("the IRN read back is %q but the raise returned %q — the freeze hook did not "+
			"store the number the operator was shown", got, irn)
	}
	if got, _ := gbody["pinned_voucher_no"].(string); got != pinned {
		t.Errorf("pinned_voucher_no read back = %q, want %q", got, pinned)
	}

	// =====================================================================
	// ⛔ THE FREEZE IS REAL. This is the assertion that proves the RE-ENTRANT
	// callback actually landed in cored's own tables rather than being
	// reported by a cooperative fake: the voucher can no longer be deleted.
	// =====================================================================
	drec, dbody := doRaw(t, st.srv, http.MethodDelete, st.ewb.base+"/vouchers/"+voucherID, st.ewb.ownerTok, nil)
	if drec.Code != http.StatusConflict {
		t.Fatalf("DELETE of an e-invoiced voucher = %d, want 409. An invoice with an IRN against "+
			"it MUST NOT be deletable (%s)", drec.Code, drec.Body.String())
	}
	frozen, _ := dbody["frozen"].(map[string]any)
	if frozen == nil || frozen["kind"] != "IRN" || frozen["ref_value"] != irn {
		t.Errorf("the refusal does not name this IRN: %v", dbody["frozen"])
	}

	// =====================================================================
	// ⛔ AN IDEMPOTENT RE-POST RETURNS THE SAME IRN. HANDOFF §1.4 B.5 item 2:
	// this is the one property whose failure mints a DUPLICATE against a real
	// government API, and it is the entire reason the workflow is durable.
	// =====================================================================
	status, again := st.raiseEInvoice(t, voucherID)
	if status != http.StatusCreated {
		t.Fatalf("re-raising = %d, want 201 (%v)", status, again)
	}
	if got, _ := again["irn"].(string); got != irn {
		t.Fatalf("a re-post returned a DIFFERENT IRN (%s, was %s). Against a real IRP that is a "+
			"second reference number for one invoice", got, irn)
	}
}

// TestTheProxyHoldsNoTransactionWhileStatutorydCallsBack is HANDOFF §1.4 B.2's
// design risk, made into a failing test rather than a comment.
//
// ⛔ WHY THIS CAN FAIL AT ALL. cored's pool here holds ONE connection (see the
// file header). Each raise reads the books in a transaction, COMMITS, and only
// then calls statutoryd — which immediately calls back into cored twice and
// needs that same single connection. Three raises run concurrently so the
// interleaving is real rather than incidental.
//
// ⛔ THE MUTATION THAT MUST TURN THIS RED: in internal/einvoice's Service.Raise,
// move the s.statutoryd.RaiseEInvoice call INSIDE the RunInTx closure. Every
// raise then holds the only connection while waiting for a callback that cannot
// get one, the deadline fires, and each request answers 503 instead of 201.
// Verified.
func TestTheProxyHoldsNoTransactionWhileStatutorydCallsBack(t *testing.T) {
	st := newEInvoiceStack(t)

	const raises = 3
	vouchers := make([]string, raises)
	for i := range vouchers {
		vouchers[i] = postInvoice(t, st.srv, st.ewb, "10.0000")
	}

	type outcome struct {
		status int
		body   map[string]any
	}
	results := make([]outcome, raises)
	var wg sync.WaitGroup
	for i, v := range vouchers {
		wg.Add(1)
		go func(i int, voucherID string) {
			defer wg.Done()
			rec, body := doRaw(t, st.srv, http.MethodPost, st.ewb.base+"/e-invoices",
				st.ewb.ownerTok, map[string]any{"voucher_id": voucherID})
			results[i] = outcome{rec.Code, body}
		}(i, v)
	}

	// A hard bound, because the failure this exists to catch is a HANG. Without
	// it a regression would present as a suite that never finishes rather than a
	// test that fails.
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(90 * time.Second):
		t.Fatalf("three concurrent e-invoice raises did not finish in 90s. The proxy is holding a " +
			"database transaction across the statutoryd call: the callback cannot get a connection " +
			"and the request hangs. Build the lines, COMMIT, then call out")
	}

	seen := map[string]bool{}
	for i, r := range results {
		if r.status != http.StatusCreated {
			t.Fatalf("concurrent raise %d = %d, want 201 (%v)", i, r.status, r.body)
		}
		irn, _ := r.body["irn"].(string)
		if irn == "" {
			t.Fatalf("concurrent raise %d came back with no IRN (%v)", i, r.body)
		}
		if seen[irn] {
			t.Errorf("two different vouchers were given the SAME IRN (%s). The workflow id is not "+
				"per-voucher", irn)
		}
		seen[irn] = true
	}
}

// TestRaisingAnEInvoiceAgainstANonInvoiceIsRefusedWithItsReason pins the
// line-item builder's refusals, which are the half of it an operator meets.
func TestRaisingAnEInvoiceAgainstANonInvoiceIsRefusedWithItsReason(t *testing.T) {
	st := newEInvoiceStack(t)

	// A receipt note: it MOVES GOODS and carries no GST at all, so it has no
	// per-line tax detail and is not an invoice.
	rrec, rbody := doRaw(t, st.srv, http.MethodPost, st.ewb.base+"/vouchers/receipt-note", st.ewb.ownerTok,
		map[string]any{"voucher_date": "2026-04-02", "created_by": "owner",
			"inventory": []any{map[string]any{"stock_item_id": st.ewb.itemID, "godown_id": st.ewb.godownID,
				"direction": "IN", "qty": "5.0000", "rate": "500.0000"}}})
	if rrec.Code != http.StatusCreated {
		t.Fatalf("receipt note = %d, want 201 (%s)", rrec.Code, rrec.Body.String())
	}
	noteID, _ := rbody["voucher_id"].(string)

	status, body := st.raiseEInvoice(t, noteID)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("raising an e-invoice against a receipt note = %d, want 422 (%v)", status, body)
	}
	msg, _ := body["error"].(string)
	// ⛔ THE REFUSAL NAMES THE CLASS. A Receipt Note is now refused for WHAT IT IS
	// rather than for what it lacks: the class gate runs before any line is read,
	// so the message an operator meets is "this is a Receipt Note and only a Sales
	// invoice can be e-invoiced", not "your tax detail is missing".
	for _, want := range []string{"Receipt Note", "SALES"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not mention %q, so it does not say WHAT was raised or what "+
				"may be:\n%s", want, msg)
		}
	}
	// ⛔ AND IT WAS REFUSED IN CORED, BEFORE statutoryd WAS TOLD ANYTHING. A
	// refusal that had already reached the IRN plane would have written a
	// workflow row for a voucher that can never be invoiced.
	st.sent.mu.Lock()
	reached := len(st.sent.last)
	st.sent.mu.Unlock()
	if reached != 0 {
		t.Errorf("cored forwarded an un-invoiceable voucher to statutoryd (%d bytes). The line "+
			"builder must refuse locally", reached)
	}

	// ---- A SALES VOUCHER WITH NO GST BLOCK AT ALL still reaches ErrNoTaxLines,
	// and that leg is here because the class gate could otherwise have swallowed
	// it: this voucher IS a sale, so the class is right and the TAX is the thing
	// that is missing. The two refusals describe different mistakes and an
	// operator acts on them differently — fix the tax, or find another voucher.
	srec, sbody := doRaw(t, st.srv, http.MethodPost, st.ewb.base+"/vouchers/sales", st.ewb.ownerTok,
		map[string]any{
			"voucher_date": "2026-06-13", "created_by": "owner",
			"party_ledger_id": st.ewb.partyID,
			"postings": []any{
				map[string]any{"ledger_id": st.ewb.partyID, "dr_cr": "D", "amount": "1000.0000"},
				map[string]any{"ledger_id": st.ewb.salesID, "dr_cr": "C", "amount": "1000.0000"},
			},
		})
	if srec.Code != http.StatusCreated {
		t.Fatalf("post an un-taxed sales voucher = %d, want 201 (%s)", srec.Code, srec.Body.String())
	}
	untaxedID, _ := sbody["voucher_id"].(string)
	status, body = st.raiseEInvoice(t, untaxedID)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("raising against a sales voucher with no GST block = %d, want 422 (%v)", status, body)
	}
	msg, _ = body["error"].(string)
	if !strings.Contains(msg, "no per-line GST detail") {
		t.Errorf("a SALES voucher carrying no tax was not refused for its missing tax detail:\n%s", msg)
	}

	// ---- CONTROL: a real item invoice on the SAME stack succeeds. Without it
	// the 422 above is consistent with the route refusing everything, which reads
	// as coverage and is not (HANDOFF §9.3).
	voucherID := postInvoice(t, st.srv, st.ewb, "5.0000")
	status, body = st.raiseEInvoice(t, voucherID)
	if status != http.StatusCreated {
		t.Fatalf("CONTROL: a real item invoice = %d, want 201. The refusal above was not caused "+
			"by the missing tax lines (%v)", status, body)
	}

	// ---- AND AN UNKNOWN VOUCHER IS A 404, NOT A 422. Distinguishing them is
	// what tells an operator whether to fix the voucher or find the right one.
	status, body = st.raiseEInvoice(t, uuid.NewString())
	if status != http.StatusNotFound {
		t.Errorf("raising against an unknown voucher = %d, want 404 (%v)", status, body)
	}
}

// TestAServiceInvoiceIsRefusedWithTheGapNamed pins the honest boundary of this
// slice.
//
// ⛔ IT ASSERTS A REFUSAL RATHER THAN A FEATURE, AND THAT IS THE POINT. A
// service invoice is an ordinary thing a business raises and NIC supports it —
// but voucher_gst_line (migration 0016) records an HSN/SAC, a value and a rate
// and NO DESCRIPTION, because it exists to feed a GSTR-1 rollup keyed on
// (hsn, rate). For an item invoice the description comes from the stock master;
// for a service line nothing in the books names it at all.
//
// The available guess — pairing GST line N with posting N and using the ledger's
// name — is not structural: the postings carry the party and the tax ledgers
// too. Guessing wrong writes one service's name against another's value on a
// document with a government reference number, and the freeze hook then makes
// the voucher un-alterable. So the builder refuses, and the message says what
// the gap is and what to do instead.
func TestAServiceInvoiceIsRefusedWithTheGapNamed(t *testing.T) {
	st := newEInvoiceStack(t)

	// An accounting-mode GST sale: real tax, real HSN/SAC, and NO inventory.
	rec, body := doRaw(t, st.srv, http.MethodPost, st.ewb.base+"/vouchers/sales", st.ewb.ownerTok,
		map[string]any{
			"voucher_date": "2026-06-12", "narration": "consultancy",
			"party_ledger_id": st.ewb.partyID, "created_by": "owner",
			"postings": []any{
				map[string]any{"ledger_id": st.ewb.partyID, "dr_cr": "D", "amount": "11800.0000"},
				map[string]any{"ledger_id": st.ewb.salesID, "dr_cr": "C", "amount": "10000.0000"},
				map[string]any{"ledger_id": st.ewb.cgstID, "dr_cr": "C", "amount": "900.0000"},
				map[string]any{"ledger_id": st.ewb.sgstID, "dr_cr": "C", "amount": "900.0000"},
			},
			"gst": map[string]any{
				"supplier_state_code":        "29",
				"place_of_supply_state_code": "29",
				"lines": []any{map[string]any{
					"taxable_value": "10000.0000", "gst_rate_pct": "18", "hsn": "998311",
				}},
				"cgst_ledger_id": st.ewb.cgstID,
				"sgst_ledger_id": st.ewb.sgstID,
			},
		})
	if rec.Code != http.StatusCreated {
		t.Fatalf("post the service invoice = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	serviceID, _ := body["voucher_id"].(string)

	status, refusal := st.raiseEInvoice(t, serviceID)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("raising an e-invoice for a service invoice = %d, want 422 (%v)", status, refusal)
	}
	msg, _ := refusal["error"].(string)
	// ⛔ THE MESSAGE IS ASSERTED, NOT JUST THE STATUS. A 422 saying nothing useful
	// is how an accountant concludes the product is broken. It must name the
	// cause and the alternative.
	for _, want := range []string{"no goods", "description", "ITEM invoice"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not mention %q, so it does not tell the operator what to "+
				"do:\n%s", want, msg)
		}
	}
	// ⛔ AND IT MUST NOT SAY "no per-line GST detail" — this voucher HAS tax
	// lines, and blaming the tax detail describes a well-formed invoice as
	// malformed. That is the ErrNoTaxLines / ErrLineNotDescribable distinction,
	// and conflating them is exactly what internal/ewaybill's ErrNoTaxLines
	// comment exists to prevent.
	if strings.Contains(msg, "no per-line GST detail") {
		t.Errorf("a service invoice was refused as having NO tax lines. It has one; what it lacks "+
			"is a description:\n%s", msg)
	}
}

// TestTheBuyerStateCodeIsThePlaceOfSupplyAndNotThePartyMaster is the test that
// DISTINGUISHES the right implementation from the plausible wrong one.
//
// ===========================================================================
// ⛔⛔ WHY THIS CASE AND NOT A REPRESENTATIVE ONE
// ===========================================================================
// The brief said to copy internal/ewaybill's loadConsignment, which reads the
// buyer's state code off the party LEDGER. Copied verbatim, that is a bug here,
// and it is invisible in every fixture where the two agree — including this
// file's other tests and every demo company.
//
// BuildEInvoice puts VoucherSnapshot.BuyerStateCode into BuyerDtls.Pos, and
// ValidateEInvoice decides intra- vs inter-State by comparing it with the
// seller's state code, then cross-checks that against the CGST/SGST/IGST split.
// That split was derived from voucher_version.place_of_supply when the voucher
// was posted. So the ONE input that tells the two implementations apart is an
// invoice where the party master's state and the voucher's place of supply
// DISAGREE — an ordinary bill-to/ship-to sale, goods delivered to a site in the
// seller's own State for a customer registered elsewhere.
//
// Right implementation: place of supply 29 == seller 29 -> intra-State ->
// CGST/SGST expected -> conformant -> 201.
// Wrong implementation: the ledger's 27 -> inter-State -> "IgstVal is zero" and
// "CgstVal/SgstVal is non-zero" -> 422, blaming the tax on a correct invoice.
func TestTheBuyerStateCodeIsThePlaceOfSupplyAndNotThePartyMaster(t *testing.T) {
	st := newEInvoiceStack(t)

	// A buyer registered in Maharashtra (27)…
	lrec, lbody := doRaw(t, st.srv, http.MethodPost, st.ewb.base+"/ledgers", st.ewb.ownerTok,
		map[string]any{
			"name": "Outstation Buyer " + uuid.NewString()[:8], "group_name": "Sundry Debtors",
			"gstin": "27AACCB4321K1Z2", "state_code": "27",
			"address": "5, Marine Drive", "city": "Mumbai", "pincode": "400020",
			"created_by": "owner",
		})
	if lrec.Code != http.StatusCreated {
		t.Fatalf("create the outstation party = %d (%s)", lrec.Code, lrec.Body.String())
	}
	outstation, _ := lbody["ledger_id"].(string)

	// …buying goods delivered IN Karnataka (29), so the supply is intra-State and
	// the tax the voucher carries is CGST + SGST.
	vrec, vbody := doRaw(t, st.srv, http.MethodPost, st.ewb.base+"/vouchers/sales", st.ewb.ownerTok,
		map[string]any{
			"voucher_date": "2026-06-11", "created_by": "owner",
			"party_ledger_id": outstation, "item_ledger_id": st.ewb.salesID,
			"gst": map[string]any{
				"supplier_state_code":        "29",
				"place_of_supply_state_code": "29",
				"cgst_ledger_id":             st.ewb.cgstID,
				"sgst_ledger_id":             st.ewb.sgstID,
			},
			"inventory": []any{map[string]any{
				"stock_item_id": st.ewb.itemID, "godown_id": st.ewb.godownID,
				"direction": "OUT", "qty": "4.0000", "rate": "500.0000",
			}},
		})
	if vrec.Code != http.StatusCreated {
		t.Fatalf("post the bill-to/ship-to invoice = %d (%s)", vrec.Code, vrec.Body.String())
	}
	voucherID, _ := vbody["voucher_id"].(string)

	status, body := st.raiseEInvoice(t, voucherID)
	if status != http.StatusCreated {
		t.Fatalf("raising an e-invoice for a bill-to/ship-to sale = %d, want 201. If the message "+
			"below names an inter-State supply, the buyer's state code is being read off the PARTY "+
			"MASTER instead of the voucher's place of supply (%v)", status, body)
	}

	// And cored really did send the place of supply, not the master's state.
	req := st.sent.lastRequest(t)
	if got, _ := req["buyer_state_code"].(string); got != "29" {
		t.Errorf("buyer_state_code = %q, want 29 — the voucher's PLACE OF SUPPLY. The party master "+
			"says 27, and sending that describes an inter-State supply the invoice did not tax as "+
			"one", got)
	}
	// ---- CONTROL: the party master really does say 27, read back through the
	// API. Without this the assertion above could be reading a value the two
	// sources happen to SHARE, and the whole test would be vacuous.
	//
	// ⛔ IT FATALS WHEN THE LEDGER CANNOT BE FOUND rather than skipping. A
	// control that quietly does nothing when a route changes shape is the failure
	// mode HANDOFF §9.2 is about: it passes forever and proves nothing.
	prec, pbody := doRaw(t, st.srv, http.MethodGet, st.ewb.base+"/ledgers", st.ewb.ownerTok, nil)
	if prec.Code != http.StatusOK {
		t.Fatalf("CONTROL: GET /ledgers = %d (%s)", prec.Code, prec.Body.String())
	}
	ledgers, _ := pbody["ledgers"].([]any)
	found := false
	for _, raw := range ledgers {
		l, _ := raw.(map[string]any)
		if id, _ := l["id"].(string); id != outstation {
			continue
		}
		found = true
		if got, _ := l["state_code"].(string); got != "27" {
			t.Fatalf("CONTROL: the party ledger's state_code reads back as %q, want 27. The two "+
				"sources are not actually different in this fixture, so the assertion above proves "+
				"nothing", got)
		}
	}
	if !found {
		t.Fatalf("CONTROL: the outstation party %s is not in GET /ledgers, so its state code was "+
			"never confirmed to differ from the place of supply", outstation)
	}
}

// TestAMissingStatutorydIsAnActionableRefusal pins the failure mode HANDOFF
// §1.4 B.2 counts as the price of proxying.
//
// ⛔ A BARE 502 IS THE WRONG ANSWER AND IS WHY THIS TEST ASSERTS THE PROSE. The
// person who fixes this is not the accountant who pressed the key; it is
// whoever runs the stack, and they need to be told the name of the process, the
// URL cored tried, and the command that starts it. This is also why the status
// is 503 and not 502: the request is retryable once the plane is back.
func TestAMissingStatutorydIsAnActionableRefusal(t *testing.T) {
	st := newEInvoiceStack(t)
	voucherID := postInvoice(t, st.srv, st.ewb, "5.0000")

	st.stopStatutoryd()

	status, body := st.raiseEInvoice(t, voucherID)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("raising with statutoryd down = %d, want 503 (%v)", status, body)
	}
	msg, _ := body["error"].(string)
	for _, want := range []string{"statutoryd", "make run-statutoryd", "STATUTORYD_BASE_URL"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not mention %q, so the operator is not told what to do "+
				"about it:\n%s", want, msg)
		}
	}
	// ⛔ AND NOTHING WAS FROZEN. A failed raise must leave the voucher editable.
	drec, _ := doRaw(t, st.srv, http.MethodDelete, st.ewb.base+"/vouchers/"+voucherID, st.ewb.ownerTok, nil)
	if drec.Code == http.StatusConflict {
		t.Errorf("the voucher was frozen even though no IRN was ever minted (%s)", drec.Body.String())
	}
}

// TestEInvoiceIsBehindStepUpAndTheCompanyPDP proves the whole reason the proxy
// lives in cored rather than the browser talking to statutoryd directly:
// statutoryd has no PDP, no RLS, no step-up and no company scoping, and cored
// has all four.
func TestEInvoiceIsBehindStepUpAndTheCompanyPDP(t *testing.T) {
	st := newEInvoiceStack(t)
	voucherID := postInvoice(t, st.srv, st.ewb, "5.0000")

	// A token for the SAME owner subject but with NO webauthn in its AMR. Every
	// write in this product answers 403 STEP_UP for one, including from curl with
	// an otherwise valid token.
	weak := mintToken(t, st.srv, "einv-nostepup-"+uuid.NewString()[:8])
	rec, _ := doRaw(t, st.srv, http.MethodPost, st.ewb.base+"/e-invoices", weak,
		map[string]any{"voucher_id": voucherID})
	if rec.Code == http.StatusCreated {
		t.Fatalf("a caller with no membership and no step-up minted an IRN. Raising an e-invoice " +
			"is an operator-facing WRITE that FREEZES a voucher; it must be behind the PDP")
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("an unauthorized raise = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}

	// ---- CONTROL: the SAME request with the Owner's stepped-up token succeeds,
	// so the 403 above is about authority and not about the request being
	// malformed.
	status, body := st.raiseEInvoice(t, voucherID)
	if status != http.StatusCreated {
		t.Fatalf("CONTROL: the Owner's raise = %d, want 201 (%v)", status, body)
	}
}

// ===========================================================================
// ⛔⛔ THE MULTI-LINE, MIXED-RATE INVOICE — AND WHY A ONE-LINE TEST IS NOT A
// TEST OF A LINE-ITEM BUILDER
// ===========================================================================
// Every assertion above runs against an invoice with EXACTLY ONE line, and on
// one line almost every wrong implementation is indistinguishable from the
// right one. A builder that ignored `ORDER BY g.line_no`, that paired the GST
// lines to the stock movements by arrival order instead of by `line_no`, that
// emitted line 1's HSN and rate for every line, or that returned the first row
// N times, would produce a byte-identical payload for a single-line invoice and
// pass everything.
//
// ⛔ THE INPUT THAT TELLS THOSE APART IS TWO LINES THAT DISAGREE ON EVERY FIELD
// THE PAIRING COULD SCRAMBLE: different stock item, different HSN, different
// GST RATE, different quantity, different unit price, different taxable value.
// Then a mis-pairing cannot cancel out — it puts 5% goods' HSN against 18%
// goods' tax, which is an e-invoice that foots correctly, passes the NIC schema,
// and describes a sale that did not happen. NIC's validator cannot catch it
// (ValidateEInvoice checks per-item presence and the invoice footing; it never
// checks that an item's tax matches its own rate), and the freeze hook makes the
// voucher un-alterable immediately afterwards.
//
// ⚠️ AND IT PINS THE ONE RECONCILIATION NIC DOES NOT DO. ValidateEInvoice checks
// TotInvVal == AssVal + CgstVal + SgstVal + IgstVal, but it NEVER checks that the
// ITEM LIST sums to AssVal. So an item list that disagrees with the invoice total
// is a document this stack would happily mint an IRN against. loadInvoice rounds
// the aggregate straight off voucher_version while loadLines rounds each line
// independently, and those two roundings are only equal by argument, not by
// construction — so the sum is asserted here TO THE PAISA rather than assumed.
func TestATwoLineMixedRateInvoicePairsEveryLineToItsOwnTaxAndFoots(t *testing.T) {
	st := newEInvoiceStack(t)

	// A SECOND item, deliberately unlike the first in HSN and in rate. 5% vs 18%
	// is the disagreement the whole test turns on.
	plate := st.addStockItem(t, "MS Plate", "7208", "5")
	st.receiveStock(t, plate, "100.0000", "200.0000")

	// Line 1: 2 @ 500 of the 18% item -> 1000 taxable, 90 + 90.
	// Line 2: 4 @ 200 of the 5%  item ->  800 taxable, 20 + 20.
	// Invoice: 1800 taxable, 110 + 110, total 2020.
	voucherID := st.postTwoLineInvoice(t, plate)

	status, body := st.raiseEInvoice(t, voucherID)
	if status != http.StatusCreated {
		t.Fatalf("raising a two-line mixed-rate invoice = %d, want 201 (%v)", status, body)
	}
	if irn, _ := body["irn"].(string); len(irn) != 64 {
		t.Fatalf("irn = %q, want a 64-char reference (%v)", body["irn"], body)
	}

	req := st.sent.lastRequest(t)
	lines, _ := req["lines"].([]any)
	if len(lines) != 2 {
		t.Fatalf("cored sent %d line(s) for a two-line invoice, want 2: %v", len(lines), req["lines"])
	}

	// ⛔ INDEXED BY POSITION AND NOT SEARCHED FOR. Looking each line up by its
	// HSN would pass an implementation that emitted them in the wrong order, and
	// the ORDER is what `ORDER BY g.line_no` exists to guarantee: NIC's SlNo is
	// positional, so line 2 of the invoice must be line 2 of the document.
	want := []map[string]string{
		{"HSN": "7216", "GstRatePct": "18", "Taxable": "1000.00", "CGST": "90.00",
			"SGST": "90.00", "IGST": "0.00", "UnitPrice": "500.00", "LineTotal": "1180.00"},
		{"HSN": "7208", "GstRatePct": "5", "Taxable": "800.00", "CGST": "20.00",
			"SGST": "20.00", "IGST": "0.00", "UnitPrice": "200.00", "LineTotal": "840.00"},
	}
	wantQty := []string{"2", "4"}
	wantDesc := []string{"MS Angle", "MS Plate"}

	for i, w := range want {
		line, _ := lines[i].(map[string]any)
		if line == nil {
			t.Fatalf("line %d is not an object: %v", i, lines[i])
		}
		for field, wantVal := range w {
			if got, _ := line[field].(string); got != wantVal {
				t.Errorf("line %d's %s = %q, want %q. If the HSN and the rate below disagree "+
					"about which line they came from, the GST lines are being paired to the "+
					"stock movements by something other than line_no", i+1, field, got, wantVal)
			}
		}
		if got, _ := line["Qty"].(string); !strings.HasPrefix(got, wantQty[i]) {
			t.Errorf("line %d's Qty = %q, want %s", i+1, got, wantQty[i])
		}
		if got, _ := line["Desc"].(string); !strings.HasPrefix(got, wantDesc[i]) {
			t.Errorf("line %d's Desc = %q, want the %q master's name. A description from the "+
				"wrong line names goods that were not sold at this price",
				i+1, got, wantDesc[i])
		}
	}

	// The invoice aggregate.
	for field, wantVal := range map[string]string{
		"taxable":       "1800.00",
		"cgst":          "110.00",
		"sgst":          "110.00",
		"igst":          "0.00",
		"invoice_total": "2020.00",
	} {
		if got, _ := req[field].(string); got != wantVal {
			t.Errorf("the request's %s = %q, want %q", field, got, wantVal)
		}
	}

	// ⛔ THE ITEM LIST MUST SUM TO THE INVOICE, TO THE PAISA. NIC does not check
	// this and neither did anything else in this file. Money is decimal here for
	// the same reason it is everywhere else in this codebase — a float64 sum of
	// two-place strings is exactly how a paisa goes missing and stays missing.
	for _, part := range []struct{ line, invoice string }{
		{"Taxable", "taxable"}, {"CGST", "cgst"}, {"SGST", "sgst"}, {"IGST", "igst"},
	} {
		sum := decimal.Zero
		for i := range lines {
			line, _ := lines[i].(map[string]any)
			raw, _ := line[part.line].(string)
			d, err := decimal.NewFromString(raw)
			if err != nil {
				t.Fatalf("line %d's %s is %q, which is not a decimal", i+1, part.line, raw)
			}
			sum = sum.Add(d)
		}
		rawTotal, _ := req[part.invoice].(string)
		total, err := decimal.NewFromString(rawTotal)
		if err != nil {
			t.Fatalf("the request's %s is %q, which is not a decimal", part.invoice, rawTotal)
		}
		if !sum.Equal(total) {
			t.Errorf("the item list's %s sums to %s but the invoice declares %s. NIC's validator "+
				"does NOT check this, so a document whose items disagree with its own total would "+
				"be minted an IRN and then frozen", part.line, sum.StringFixed(2), total.StringFixed(2))
		}
	}
}

// TestTheItemListFootsToTheInvoiceWhenTheBOOKSCARRYFOURDECIMALS is the paisa
// case, and it is the one the round numbers above cannot reach.
//
// ===========================================================================
// ⛔⛔ WHY A TEST WITH DELIBERATELY UGLY NUMBERS
// ===========================================================================
// The books hold NUMERIC(19,4) and NIC prints two places, so SOMETHING must
// round. There are two places to do it and they do not agree:
//
//	round(SUM(line taxables))   -- what voucher_version.taxable_value gives
//	SUM(round(line taxable))    -- what the ITEM LIST on the document adds up to
//
// 3 units at 10.0050 is 30.0150 a line. Two such lines: each rounds to 30.02, so
// the item list sums to 60.04 — while the aggregate 60.0300 rounds to 60.03.
// ⛔ THE DOCUMENT THEN DECLARES A TAXABLE VALUE ITS OWN ITEMS DO NOT ADD UP TO,
// and it is off by exactly one paisa, which is the amount nobody notices.
//
// ⚠️ AND NOTHING IN THIS STACK WOULD CATCH IT. ValidateEInvoice checks the
// footing TotInvVal == AssVal + CgstVal + SgstVal + IgstVal — which still holds,
// because every one of those four is the aggregate — and it never checks the
// item list against AssVal. The REAL IRP does ("sum of AssAmt of all items must
// equal AssVal"), so this is not a stylistic preference: it is a payload the fake
// provider mints an IRN for and the National Informatics Centre rejects.
//
// The test is written against the ONE number a reader can check by hand rather
// than against a tolerance, because a tolerance here is how the paisa hides.
func TestTheItemListFootsToTheInvoiceWhenTheBOOKSCARRYFOURDECIMALS(t *testing.T) {
	st := newEInvoiceStack(t)

	second := st.addStockItem(t, "MS Rod", "7213", "18")
	st.receiveStock(t, second, "100.0000", "10.0050")

	voucherID := st.postFourDecimalInvoice(t, second)

	status, body := st.raiseEInvoice(t, voucherID)
	if status != http.StatusCreated {
		t.Fatalf("raising a four-decimal invoice = %d, want 201 (%v)", status, body)
	}

	req := st.sent.lastRequest(t)
	lines, _ := req["lines"].([]any)
	if len(lines) != 2 {
		t.Fatalf("cored sent %d line(s), want 2: %v", len(lines), req["lines"])
	}

	sum := decimal.Zero
	for i := range lines {
		line, _ := lines[i].(map[string]any)
		raw, _ := line["Taxable"].(string)
		d, err := decimal.NewFromString(raw)
		if err != nil {
			t.Fatalf("line %d's Taxable is %q, which is not a decimal", i+1, raw)
		}
		sum = sum.Add(d)
	}

	rawTotal, _ := req["taxable"].(string)
	declared, err := decimal.NewFromString(rawTotal)
	if err != nil {
		t.Fatalf("the request's taxable is %q, which is not a decimal", rawTotal)
	}
	if !sum.Equal(declared) {
		t.Fatalf("the item list's taxable values sum to %s but the invoice declares %s — a "+
			"difference of %s. The aggregate is being rounded independently of the lines, so the "+
			"document does not add up to itself. Derive the invoice figures from the ROUNDED "+
			"LINES, which is what the item list is",
			sum.StringFixed(2), declared.StringFixed(2), sum.Sub(declared).StringFixed(2))
	}

	// ---- AND THE NIC FOOTING STILL HOLDS: total == taxable + taxes. Fixing the
	// item-list sum by moving the taxable and breaking this would be trading one
	// inconsistency for another, so both are asserted together.
	var cgst, sgst, igst, total decimal.Decimal
	for name, into := range map[string]*decimal.Decimal{
		"cgst": &cgst, "sgst": &sgst, "igst": &igst, "invoice_total": &total,
	} {
		raw, _ := req[name].(string)
		d, err := decimal.NewFromString(raw)
		if err != nil {
			t.Fatalf("the request's %s is %q, which is not a decimal", name, raw)
		}
		*into = d
	}
	if want := declared.Add(cgst).Add(sgst).Add(igst); !total.Equal(want) {
		t.Errorf("invoice_total = %s but taxable + cgst + sgst + igst = %s. NIC enforces this "+
			"footing and refuses the document without it", total.StringFixed(2), want.StringFixed(2))
	}
}

// postFourDecimalInvoice posts an intra-State item invoice whose two lines each
// have a FOUR-DECIMAL taxable value that rounds UP (3 x 10.0050 = 30.0150).
// Chosen so that summing the rounded lines and rounding the summed lines give
// different answers; see the test above.
func (s *einvStack) postFourDecimalInvoice(t *testing.T, secondItemID string) string {
	t.Helper()
	rec, body := doRaw(t, s.srv, http.MethodPost, s.ewb.base+"/vouchers/sales", s.ewb.ownerTok,
		map[string]any{
			"voucher_date":    "2026-06-12",
			"created_by":      "owner",
			"party_ledger_id": s.ewb.partyID,
			"item_ledger_id":  s.ewb.salesID,
			"gst": map[string]any{
				"supplier_state_code":        "29",
				"place_of_supply_state_code": "29",
				"cgst_ledger_id":             s.ewb.cgstID,
				"sgst_ledger_id":             s.ewb.sgstID,
			},
			"inventory": []any{
				map[string]any{"stock_item_id": s.ewb.itemID, "godown_id": s.ewb.godownID,
					"direction": "OUT", "qty": "3.0000", "rate": "10.0050"},
				map[string]any{"stock_item_id": secondItemID, "godown_id": s.ewb.godownID,
					"direction": "OUT", "qty": "3.0000", "rate": "10.0050"},
			},
		})
	if rec.Code != http.StatusCreated {
		t.Fatalf("post the four-decimal item invoice = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	id, _ := body["voucher_id"].(string)
	if id == "" {
		t.Fatalf("the four-decimal invoice came back with no voucher_id: %s", rec.Body.String())
	}
	return id
}

// addStockItem creates a stock item over HTTP and returns its id. The HSN and
// the GST rate come off the MASTER on an item invoice and never off the request
// (ledger/item_invoice.go), which is why they are set here and nowhere else.
//
// ⛔ AND SO DOES THE UNIT. NIC's ItemList[].Unit is mandatory for goods, and
// stock_item.unit_id is nullable — so an item created without one is refused by
// einvoice.ErrNoUnitOfMeasure rather than shipping a blank. That refusal has its
// own test; every item built here is a well-formed master.
func (s *einvStack) addStockItem(t *testing.T, name, hsn, ratePct string) string {
	t.Helper()
	return s.addStockItemWithUnit(t, name, hsn, ratePct, "Nos")
}

// addStockItemWithUnit is addStockItem with the unit spelled out. A blank unit
// is a real (and refused) state of the master, so the tests that exercise it
// need a way to create one.
func (s *einvStack) addStockItemWithUnit(t *testing.T, name, hsn, ratePct, unit string) string {
	t.Helper()
	req := map[string]any{
		"name":         name + " " + uuid.NewString()[:8],
		"hsn":          hsn,
		"gst_rate_pct": ratePct,
		"created_by":   "owner",
	}
	if unit != "" {
		req["unit_symbol"] = unit
	}
	rec, body := doRaw(t, s.srv, http.MethodPost, s.ewb.base+"/stock-items", s.ewb.ownerTok, req)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("create stock item %q = %d (%s)", name, rec.Code, rec.Body.String())
	}
	id, _ := body["stock_item_id"].(string)
	if id == "" {
		id, _ = body["id"].(string)
	}
	if id == "" {
		t.Fatalf("stock item %q came back with no id: %s", name, rec.Body.String())
	}
	return id
}

// receiveStock puts goods on the shelf over HTTP. Selling out of an empty
// warehouse is refused by stock_movement_not_negative, which would be a refusal
// for the wrong reason (HANDOFF §9.3).
func (s *einvStack) receiveStock(t *testing.T, itemID, qty, rate string) {
	t.Helper()
	rec, _ := doRaw(t, s.srv, http.MethodPost, s.ewb.base+"/vouchers/receipt-note", s.ewb.ownerTok,
		map[string]any{"voucher_date": "2026-04-01", "created_by": "owner",
			"inventory": []any{map[string]any{
				"stock_item_id": itemID, "godown_id": s.ewb.godownID,
				"direction": "IN", "qty": qty, "rate": rate}}})
	if rec.Code != http.StatusCreated {
		t.Fatalf("opening receipt note = %d (%s)", rec.Code, rec.Body.String())
	}
}

// postTwoLineInvoice posts an intra-State item invoice whose two lines disagree
// on item, HSN, rate, quantity and price. Line ORDER is the request's order, and
// ledger.go writes voucher_gst_line.line_no as the 1-based index of the derived
// tax lines, so line 1 here is line 1 there.
func (s *einvStack) postTwoLineInvoice(t *testing.T, secondItemID string) string {
	t.Helper()
	rec, body := doRaw(t, s.srv, http.MethodPost, s.ewb.base+"/vouchers/sales", s.ewb.ownerTok,
		map[string]any{
			"voucher_date":    "2026-06-11",
			"created_by":      "owner",
			"party_ledger_id": s.ewb.partyID,
			"item_ledger_id":  s.ewb.salesID,
			"gst": map[string]any{
				"supplier_state_code":        "29",
				"place_of_supply_state_code": "29",
				"cgst_ledger_id":             s.ewb.cgstID,
				"sgst_ledger_id":             s.ewb.sgstID,
			},
			"inventory": []any{
				map[string]any{"stock_item_id": s.ewb.itemID, "godown_id": s.ewb.godownID,
					"direction": "OUT", "qty": "2.0000", "rate": "500.0000"},
				map[string]any{"stock_item_id": secondItemID, "godown_id": s.ewb.godownID,
					"direction": "OUT", "qty": "4.0000", "rate": "200.0000"},
			},
		})
	if rec.Code != http.StatusCreated {
		t.Fatalf("post the two-line item invoice = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	id, _ := body["voucher_id"].(string)
	if id == "" {
		t.Fatalf("the two-line invoice came back with no voucher_id: %s", rec.Body.String())
	}
	return id
}

// ===========================================================================
// ⛔⛔ THE CLASS GATE — A PURCHASE COULD BE e-INVOICED, AND WAS THEN FROZEN
// ===========================================================================
// loadInvoice read no voucher type at all. It refused receipts, journals and
// service invoices, but only as a SIDE EFFECT of their having no GST lines or no
// goods — so anything carrying both was accepted, and that set includes
// PURCHASE, CREDIT NOTE and DEBIT NOTE.
//
// ⛔ WHY THIS IS THE INPUT THAT DISTINGUISHES RIGHT FROM WRONG. Every other test
// in this file posts a SALES invoice, and on a sales invoice the two
// implementations are byte-identical: reading the class and not reading it agree
// on every payload the suite ever built. A PURCHASE is the one document where
// they disagree, and the disagreement is not subtle:
//
//	SELLER gstin sent = the company's OWN      (it comes off the `company` row)
//	BUYER  gstin sent = its SUPPLIER's
//
// i.e. a government-referenced document asserting that this company SOLD goods
// to its own vendor. The freeze hook then makes the purchase record un-alterable
// and un-deletable, and there is no undo anywhere in this product — which is why
// the last leg here is the DELETE.
func TestOnlyAnOutwardSalesInvoiceCanBeEInvoiced(t *testing.T) {
	st := newEInvoiceStack(t)
	p := st.tradeLedgers(t)

	for _, tc := range []struct {
		what     string
		voucher  func(t *testing.T) string
		wantName string
	}{
		{
			what:     "a PURCHASE (the seller/buyer identity is inverted)",
			voucher:  func(t *testing.T) string { return st.postPurchaseInvoice(t, p) },
			wantName: "Purchase",
		},
		{
			// ⚠️ A CREDIT NOTE IS LEGITIMATELY e-INVOICEABLE IN INDIA, as NIC
			// document type CRN. It is refused here because this build emits only
			// "INV" and models no reference to the original invoice — so the
			// alternative to refusing is submitting a credit note AS a tax invoice,
			// which also collides with Sales #1 at the IRP (one NIC key:
			// SupplierGSTIN + DocTyp + DocNo + FY). The refusal names the gap.
			what:     "a CREDIT NOTE (a real CRN document this build does not emit)",
			voucher:  func(t *testing.T) string { return st.postCreditNoteInvoice(t) },
			wantName: "Credit Note",
		},
	} {
		t.Run(tc.what, func(t *testing.T) {
			voucherID := tc.voucher(t)

			status, body := st.raiseEInvoice(t, voucherID)
			if status != http.StatusUnprocessableEntity {
				t.Fatalf("raising an e-invoice against %s = %d, want 422. An IRN minted here is a "+
					"document about a sale that never happened, and the freeze hook makes it "+
					"permanent (%v)", tc.what, status, body)
			}
			msg, _ := body["error"].(string)
			// ⛔ THE MESSAGE IS ASSERTED, NOT ONLY THE STATUS: a refusal that names
			// the class beats a silent success on the wrong document, and it also
			// beats an unexplained 422 on a voucher that is perfectly well-formed.
			for _, want := range []string{tc.wantName, "SALES"} {
				if !strings.Contains(msg, want) {
					t.Errorf("the refusal does not mention %q, so it does not tell the operator what "+
						"they raised or what may be raised:\n%s", want, msg)
				}
			}

			// ⛔ AND NOTHING REACHED statutoryd. A refusal that had already crossed
			// into the IRN plane would have written a durable workflow row against a
			// voucher that can never be invoiced.
			st.sent.mu.Lock()
			reached := len(st.sent.last)
			st.sent.mu.Unlock()
			if reached != 0 {
				t.Errorf("cored forwarded %s to statutoryd (%d bytes)", tc.what, reached)
			}

			// ⛔⛔ AND THE VOUCHER IS STILL DELETABLE. This is the assertion that
			// makes the defect concrete rather than stylistic: before the class gate
			// the raise answered 201 and this DELETE answered 409
			// `voucher frozen by IRN …`, permanently, on a legitimate record.
			drec, dbody := doRaw(t, st.srv, http.MethodDelete, st.ewb.base+"/vouchers/"+voucherID,
				st.ewb.ownerTok, nil)
			if drec.Code == http.StatusConflict {
				t.Fatalf("%s was FROZEN by a refused e-invoice: %v", tc.what, dbody)
			}
		})
	}

	// ---- CONTROL: a SALES item invoice on the very same stack still succeeds.
	// Without it every 422 above is consistent with a class gate that refuses
	// everything, which reads as coverage and is not (HANDOFF §9.3).
	voucherID := postInvoice(t, st.srv, st.ewb, "5.0000")
	status, body := st.raiseEInvoice(t, voucherID)
	if status != http.StatusCreated {
		t.Fatalf("CONTROL: a SALES item invoice = %d, want 201. The refusals above were not caused "+
			"by the voucher CLASS (%v)", status, body)
	}
}

// ===========================================================================
// ⛔⛔ THE DISCOUNTED LINE — A ₹500 ARITHMETIC CONTRADICTION ON THE DOCUMENT
// ===========================================================================
// NIC's item block is an arithmetic statement: TotAmt = Qty x UnitPrice and
// AssAmt = TotAmt - Discount. The assembler sent the GROSS rate as UnitPrice and
// the NET-of-discount value as both TotAmt and AssAmt, with no Discount field in
// the snapshot at all. On every invoice in this file that was invisible, because
// every one of them has no discount and the gross IS the net.
//
// ⛔ SO THE INPUT THAT DISTINGUISHES THEM IS A DISCOUNT, AND NOTHING ELSE DOES.
// `discount_pct` is on the public wire (server.go's inventory line) and
// InventoryLine.TaxableValue is qty x rate x (1 - disc), so:
//
//	10 units @ 500.00, 10% off  ->  Qty x UnitPrice = 5000.00
//	                               declared TotAmt  = 4500.00, Discount absent
//
// Five hundred rupees of self-contradiction, in a document that then foots
// perfectly at the invoice level and is frozen onto the voucher. This track
// chased a ONE-PAISA divergence and shipped this.
func TestADiscountedLineDeclaresItsGrossAndTheDiscountItTook(t *testing.T) {
	st := newEInvoiceStack(t)
	voucherID := st.postDiscountedInvoice(t)

	status, body := st.raiseEInvoice(t, voucherID)
	if status != http.StatusCreated {
		t.Fatalf("raising a DISCOUNTED item invoice = %d, want 201 (%v)", status, body)
	}

	req := st.sent.lastRequest(t)
	lines, _ := req["lines"].([]any)
	if len(lines) != 1 {
		t.Fatalf("cored sent %d line(s), want 1: %v", len(lines), req["lines"])
	}
	line, _ := lines[0].(map[string]any)

	// ⛔ RECONCILED TO THE PAISA, NOT WITHIN A TOLERANCE. 10 @ 500 gross is
	// 5000.00; 10% off is 500.00; the taxable base is 4500.00 and 18% intra-State
	// is 405.00 + 405.00.
	for field, want := range map[string]string{
		"UnitPrice": "500.00",  // the GROSS rate, which is what NIC multiplies
		"Discount":  "500.00",  // ⛔ was absent entirely
		"Taxable":   "4500.00", // the books' own taxable value, unchanged
		"CGST":      "405.00",
		"SGST":      "405.00",
		"IGST":      "0.00",
		"LineTotal": "5310.00",
	} {
		if got, _ := line[field].(string); got != want {
			t.Errorf("the discounted line's %s = %q, want %q", field, got, want)
		}
	}

	// ⛔⛔ AND THE IDENTITY ITSELF, COMPUTED HERE RATHER THAN COPIED FROM A
	// CONSTANT: Qty x UnitPrice - Discount must be the taxable value. Asserting
	// the three literals above without this leg would pass an implementation that
	// emitted a plausible discount unrelated to the price.
	qty := mustDecimal(t, line["Qty"])
	unitPrice := mustDecimal(t, line["UnitPrice"])
	discount := mustDecimal(t, line["Discount"])
	taxable := mustDecimal(t, line["Taxable"])
	if gross := qty.Mul(unitPrice).Round(2); !gross.Sub(discount).Equal(taxable) {
		t.Errorf("Qty x UnitPrice = %s, less Discount %s, is %s — but the line declares a taxable "+
			"value of %s. NIC requires AssAmt = TotAmt - Discount and the IRP refuses a document "+
			"whose items do not say so", gross.StringFixed(2), discount.StringFixed(2),
			gross.Sub(discount).StringFixed(2), taxable.StringFixed(2))
	}

	// The invoice figures are the item list's own footing, discount and all.
	for field, want := range map[string]string{
		"taxable": "4500.00", "cgst": "405.00", "sgst": "405.00", "igst": "0.00",
		"invoice_total": "5310.00",
	} {
		if got, _ := req[field].(string); got != want {
			t.Errorf("the request's %s = %q, want %q", field, got, want)
		}
	}
}

// ⛔⛔ A LINE WHOSE PRECISION CANNOT BE STATED AT NIC'S TWO DECIMALS IS REFUSED
// BY NAME — not with a message blaming a discount that does not exist.
//
// NIC's item block asserts BOTH `TotAmt = Qty x UnitPrice` AND
// `AssAmt = TotAmt - Discount`, at two decimals. The books keep four, and
// `voucher_gst_line.taxable_value` (the figure GSTR-1 is filed on) has already
// been rounded to four by Postgres. On a .xx5 boundary the two round in
// OPPOSITE directions and the identities cannot both hold:
//
//	qty 0.0011 x rate 10913.6000 = 12.00496 exactly
//	  books:  round4 -> 12.0050         -> AssAmt 12.01
//	  NIC:    round2 of the product     -> TotAmt 12.00
//	  => Discount -0.01, and NIC has no surcharge field.
//
// ⚠️ THIS TEST EXISTS BECAUSE THE DISCOUNT FIX FIRST SHIPPED A CONFUSING
// REFUSAL OF IT: an invoice carrying NO DISCOUNT AT ALL was rejected with
// "ItemList[0].Discount: must not be negative". Rounding the gross to four
// places first was then tried and only MOVED the message to
// "TotAmt: must equal Qty x UnitPrice". Neither is a fix, because there is
// nothing to fix — the constraint is genuinely unsatisfiable. What was wrong
// was the DIAGNOSIS the operator was handed.
//
// ⛔ So this asserts the refusal AND its wording. A 422 alone would pass on
// either of the two misleading messages above, which is precisely how this
// shipped in the first place.
func TestALineWhosePrecisionNICCannotExpressIsRefusedByName(t *testing.T) {
	st := newEInvoiceStack(t)
	voucherID := st.postInvoiceForItem(t, st.ewb.itemID, "0.0011", "10913.6000", "")

	status, body := st.raiseEInvoice(t, voucherID)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("raising an invoice whose line cannot be stated at two decimals = %d, want 422 "+
			"(%v)", status, body)
	}
	msg, _ := body["error"].(string)
	// ⛔ IT MUST NAME THE REAL CAUSE. The operator cannot act on "your discount is
	// negative" when they entered no discount.
	for _, want := range []string{
		"cannot be stated in NIC's two-decimal item block",
		"12.00", // the gross NIC would compute
		"12.01", // the taxable value the books hold
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not mention %q — it must give the operator both figures "+
				"so they can see which way the rounding went. Got: %s", want, msg)
		}
	}
	// ⛔ AND IT MUST NOT BLAME A DISCOUNT. This is the assertion that fails on
	// both of the messages this defect actually shipped.
	if strings.Contains(msg, "Discount: must not be negative") {
		t.Errorf("the refusal blames a negative discount on a voucher that carries NO discount: %s", msg)
	}

	// ⛔ THE CONTROL: nothing was minted and nothing was frozen. A refusal that
	// still pins an IRN would be worse than the payload it refused, because the
	// freeze is irreversible.
	if !st.sent.nothingSent() {
		t.Errorf("cored POSTed to statutoryd for a voucher it refused — the freeze hook pins an " +
			"IRN irreversibly, so a refusal must not reach it at all")
	}
}

// ⛔ THE CONTROL FOR THE TEST ABOVE, and without it that refusal could be
// refusing every invoice in the product. An ordinary un-discounted line whose
// product does NOT sit on a boundary must still raise.
func TestAnOrdinaryUndiscountedLineStillRaises(t *testing.T) {
	st := newEInvoiceStack(t)
	voucherID := st.postInvoiceForItem(t, st.ewb.itemID, "10.0000", "500.0000", "")

	status, body := st.raiseEInvoice(t, voucherID)
	if status != http.StatusCreated {
		t.Fatalf("raising an ordinary un-discounted invoice = %d, want 201 (%v)", status, body)
	}
	line, _ := st.sent.lastRequest(t)["lines"].([]any)
	first, _ := line[0].(map[string]any)
	if got, _ := first["Discount"].(string); got != "0.00" {
		t.Errorf("an un-discounted line's Discount = %q, want %q", got, "0.00")
	}
}

// mustDecimal parses a wire money string as a decimal or fails the test.
//
// ⛔ decimal, NEVER float64. A test that reconciles a document with ParseFloat is
// a test that cannot see the paisa it exists to protect.
func mustDecimal(t *testing.T, raw any) decimal.Decimal {
	t.Helper()
	s, _ := raw.(string)
	d, err := decimal.NewFromString(s)
	if err != nil {
		t.Fatalf("%q is not a decimal", s)
	}
	return d
}

// TestAnItemWithNoUnitOfMeasureIsRefusedRatherThanShippingABlankUnit pins the
// third silent hole: NIC's ItemList[].Unit is MANDATORY for goods, stock_item's
// unit is NULLABLE, and the assembler used to COALESCE the NULL to "".
//
// ⛔ THE REFUSAL IS THE FEATURE. "NOS" is the tempting default and it is a claim
// about the goods — ten NOS of a thing sold by the kilogram is a different
// consignment from ten kilograms, and nothing on the face of the document shows
// which one was meant. It is the same argument the service-invoice refusal makes
// about a guessed description, and it gets the same answer, because the freeze
// hook makes the voucher un-alterable the instant the IRN lands.
func TestAnItemWithNoUnitOfMeasureIsRefusedRatherThanShippingABlankUnit(t *testing.T) {
	st := newEInvoiceStack(t)

	// A master with NO unit — which is what the whole suite's own fixture used to
	// be, and why this shipped unnoticed.
	unitless := st.addStockItemWithUnit(t, "MS Billet", "7207", "18", "")
	st.receiveStock(t, unitless, "50.0000", "300.0000")
	voucherID := st.postInvoiceForItem(t, unitless, "2.0000", "300.0000", "")

	status, body := st.raiseEInvoice(t, voucherID)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("raising an e-invoice for goods with no unit = %d, want 422. NIC requires a unit "+
			"on every goods line and a blank one would be minted and then frozen (%v)", status, body)
	}
	msg, _ := body["error"].(string)
	for _, want := range []string{"unit of measure", "unit_symbol", "MS Billet"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not mention %q, so it does not say which item to fix or "+
				"how:\n%s", want, msg)
		}
	}

	// ---- CONTROL: the SAME invoice shape against an item that HAS a unit is a
	// 201, and the unit reaches the wire in NIC's own upper case. Without this leg
	// the 422 above is consistent with the assembler refusing every second item,
	// and the blank would be replaced by a different silent failure.
	sound := st.addStockItemWithUnit(t, "MS Billet Sound", "7207", "18", "Nos")
	st.receiveStock(t, sound, "50.0000", "300.0000")
	okVoucher := st.postInvoiceForItem(t, sound, "2.0000", "300.0000", "")
	status, body = st.raiseEInvoice(t, okVoucher)
	if status != http.StatusCreated {
		t.Fatalf("CONTROL: the same invoice with a unit on the master = %d, want 201. The refusal "+
			"above was not caused by the missing unit (%v)", status, body)
	}
	req := st.sent.lastRequest(t)
	lines, _ := req["lines"].([]any)
	if len(lines) != 1 {
		t.Fatalf("cored sent %d line(s), want 1", len(lines))
	}
	line, _ := lines[0].(map[string]any)
	if got, _ := line["Unit"].(string); got != "NOS" {
		t.Errorf("the line's Unit = %q, want NOS — NIC's UQCs are an upper-case vocabulary and the "+
			"master spells it \"Nos\"", got)
	}
}

// tradeLedgers is the purchase side of the books: a supplier, a purchase
// account and the INPUT tax heads. The e-way bill fixture carries only the sales
// side, because until now nothing in this file could post a purchase.
type tradeLedgers struct {
	supplierID string
	purchaseID string
	icgstID    string
	isgstID    string
}

func (s *einvStack) tradeLedgers(t *testing.T) tradeLedgers {
	t.Helper()
	mk := func(name, group string, extra map[string]any) string {
		t.Helper()
		body := map[string]any{
			"name": name + " " + uuid.NewString()[:8], "group_name": group, "created_by": "owner",
		}
		for k, v := range extra {
			body[k] = v
		}
		rec, out := doRaw(t, s.srv, http.MethodPost, s.ewb.base+"/ledgers", s.ewb.ownerTok, body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create ledger %q = %d (%s)", name, rec.Code, rec.Body.String())
		}
		id, _ := out["ledger_id"].(string)
		return id
	}
	return tradeLedgers{
		// The supplier carries its OWN GSTIN, and that is the field the defect
		// turns on: e-invoiced as a sale, this GSTIN lands in BuyerDtls while the
		// company's own lands in SellerDtls.
		supplierID: mk("Acme Supplier", "Sundry Creditors", map[string]any{
			"gstin": "29AACCV9999K1Z9", "state_code": "29",
			"address": "22, Mysore Road", "city": "Bengaluru", "pincode": "560026",
		}),
		purchaseID: mk("Purchase A/c", "Purchase Accounts", nil),
		icgstID:    mk("Input CGST", "Duties & Taxes", nil),
		isgstID:    mk("Input SGST", "Duties & Taxes", nil),
	}
}

// postPurchaseInvoice posts a PURCHASE item invoice: real goods, real GST lines,
// a real supplier. Structurally identical to the sales invoice every other test
// here raises — which is exactly why the assembler could not tell them apart.
func (s *einvStack) postPurchaseInvoice(t *testing.T, p tradeLedgers) string {
	t.Helper()
	rec, body := doRaw(t, s.srv, http.MethodPost, s.ewb.base+"/vouchers/purchase", s.ewb.ownerTok,
		map[string]any{
			"voucher_date": "2026-06-14", "created_by": "owner",
			"party_ledger_id": p.supplierID, "item_ledger_id": p.purchaseID,
			"gst": map[string]any{
				"supplier_state_code":        "29",
				"place_of_supply_state_code": "29",
				"cgst_ledger_id":             p.icgstID,
				"sgst_ledger_id":             p.isgstID,
			},
			// Goods ARRIVE on a purchase; the direction is the class's, not a choice.
			"inventory": []any{map[string]any{
				"stock_item_id": s.ewb.itemID, "godown_id": s.ewb.godownID,
				"direction": "IN", "qty": "3.0000", "rate": "400.0000",
			}},
		})
	if rec.Code != http.StatusCreated {
		t.Fatalf("post the purchase item invoice = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	id, _ := body["voucher_id"].(string)
	return id
}

// postCreditNoteInvoice posts a CREDIT NOTE with goods coming back from the
// customer — a sales return, which is a real NIC document (CRN) this build does
// not emit.
func (s *einvStack) postCreditNoteInvoice(t *testing.T) string {
	t.Helper()
	rec, body := doRaw(t, s.srv, http.MethodPost, s.ewb.base+"/vouchers/credit-note", s.ewb.ownerTok,
		map[string]any{
			"voucher_date": "2026-06-15", "created_by": "owner",
			"party_ledger_id": s.ewb.partyID, "item_ledger_id": s.ewb.salesID,
			"gst": map[string]any{
				"supplier_state_code":        "29",
				"place_of_supply_state_code": "29",
				"cgst_ledger_id":             s.ewb.cgstID,
				"sgst_ledger_id":             s.ewb.sgstID,
			},
			"inventory": []any{map[string]any{
				"stock_item_id": s.ewb.itemID, "godown_id": s.ewb.godownID,
				"direction": "IN", "qty": "1.0000", "rate": "500.0000",
			}},
		})
	if rec.Code != http.StatusCreated {
		t.Fatalf("post the credit note = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	id, _ := body["voucher_id"].(string)
	return id
}

// postDiscountedInvoice posts the sales item invoice the discount case turns on:
// ten units at 500 with 10% off the line.
func (s *einvStack) postDiscountedInvoice(t *testing.T) string {
	t.Helper()
	return s.postInvoiceForItem(t, s.ewb.itemID, "10.0000", "500.0000", "10")
}

// postInvoiceForItem posts a one-line intra-State sales item invoice for a
// NAMED item, with an optional line discount. It is the shared shape behind the
// discount and unit-of-measure cases; postInvoice (the e-way bill fixture's) can
// only ever bill the fixture's own item at 500.
func (s *einvStack) postInvoiceForItem(t *testing.T, itemID, qty, rate, discountPct string) string {
	t.Helper()
	line := map[string]any{
		"stock_item_id": itemID, "godown_id": s.ewb.godownID,
		"direction": "OUT", "qty": qty, "rate": rate,
	}
	if discountPct != "" {
		line["discount_pct"] = discountPct
	}
	rec, body := doRaw(t, s.srv, http.MethodPost, s.ewb.base+"/vouchers/sales", s.ewb.ownerTok,
		map[string]any{
			"voucher_date": "2026-06-16", "created_by": "owner",
			"party_ledger_id": s.ewb.partyID, "item_ledger_id": s.ewb.salesID,
			"gst": map[string]any{
				"supplier_state_code":        "29",
				"place_of_supply_state_code": "29",
				"cgst_ledger_id":             s.ewb.cgstID,
				"sgst_ledger_id":             s.ewb.sgstID,
			},
			"inventory": []any{line},
		})
	if rec.Code != http.StatusCreated {
		t.Fatalf("post the item invoice = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	id, _ := body["voucher_id"].(string)
	if id == "" {
		t.Fatalf("the item invoice came back with no voucher_id: %s", rec.Body.String())
	}
	return id
}
