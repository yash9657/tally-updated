package server_test

// draft_accept_http_test.go — §7.3 B ON THE WIRE.
//
// internal/ledger's draft_acceptance_test.go proves the DATABASE refuses a second
// accept of one insightd bill draft. This file proves the two things that decide
// whether the browser can act on that refusal, and neither is visible from the
// ledger:
//
//	1. `draft_id` REACHES THE LEDGER FROM A JSON BODY AT ALL. This is the
//	   reachability half. voucherReq.ItemLedgerID and voucherReq.CostAllocations
//	   both carry comments about features that were fully built, fully tested and
//	   completely unreachable because no wire field carried them — nothing failed
//	   and no test noticed, because every layer under test was fine.
//
//	2. THE REFUSAL IS SHAPED THE WAY THE CLIENT PARSES IT. draftAccept.ts reads
//	   `body.detail.code` on a 409 and translates `draft_already_accepted` into a
//	   sentence naming the voucher. cored's usual {"error": ...} would still refuse
//	   — but as an unrecognised conflict, so the operator would be told their bill
//	   was refused without being told which voucher it is already on.
//
// Every step is a real HTTP request, exactly as the browser makes it.

import (
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/platform/testdb"
	"github.com/khata/cored/internal/server"
)

type draftAcceptFixture struct {
	base     string
	ownerTok string
	subject  string

	partyID    string
	purchaseID string
}

func newDraftAcceptFixture(t *testing.T, srv *server.Server) draftAcceptFixture {
	t.Helper()
	subject := "draft-owner-" + uuid.NewString()[:8]
	ownerTok := mintToken(t, srv, subject, "webauthn")
	cid := uuid.New()
	companyID := cid.String()

	rec, _ := doRaw(t, srv, http.MethodPost, "/v1/companies", ownerTok, map[string]any{
		"id": companyID, "name": "Draft Co " + companyID[:8],
		"fy_start_date": "2026-04-01", "base_currency": "INR", "state_code": "29",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create company = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	testdb.Cleanup(t, cid)

	f := draftAcceptFixture{base: "/v1/companies/" + companyID, ownerTok: ownerTok, subject: subject}
	mk := func(name, group string) string {
		t.Helper()
		lrec, lb := doRaw(t, srv, http.MethodPost, f.base+"/ledgers", ownerTok, map[string]any{
			"name": name, "group_name": group, "created_by": subject,
		})
		if lrec.Code != http.StatusCreated {
			t.Fatalf("create ledger %q = %d (%s)", name, lrec.Code, lrec.Body.String())
		}
		id, _ := lb["ledger_id"].(string)
		return id
	}
	f.partyID = mk("Pune Fabricators", "Sundry Creditors")
	f.purchaseID = mk("Local Purchases", "Purchase Accounts")
	return f
}

// acceptBody is what the Bill Inbox POSTs: a balanced purchase plus the draft it
// came from. `draftID` empty is the hand-keyed body every other screen sends.
func (f draftAcceptFixture) acceptBody(draftID, amount string) map[string]any {
	body := map[string]any{
		"voucher_date": "2026-06-19", "party": "Pune Fabricators",
		"party_ledger_id": f.partyID, "created_by": f.subject,
		"postings": []any{
			map[string]any{"ledger_id": f.purchaseID, "dr_cr": "D", "amount": amount},
			map[string]any{"ledger_id": f.partyID, "dr_cr": "C", "amount": amount},
		},
	}
	if draftID != "" {
		body["draft_id"] = draftID
	}
	return body
}

// dayBookCount is how many vouchers the books actually hold. The assertion that
// matters is never "a 409 came back" — it is that the refused accept left nothing.
func (f draftAcceptFixture) dayBookCount(t *testing.T, srv *server.Server) int {
	t.Helper()
	rec, b := doRaw(t, srv, http.MethodGet, f.base+"/day-book", f.ownerTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("day book = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	entries, _ := b["entries"].([]any)
	return len(entries)
}

// TestASecondAcceptOfOneDraftIs409WithTheCodeTheClientParses.
func TestASecondAcceptOfOneDraftIs409WithTheCodeTheClientParses(t *testing.T) {
	srv := newServer(t)
	f := newDraftAcceptFixture(t, srv)

	rec, first := doRaw(t, srv, http.MethodPost, f.base+"/vouchers/purchase",
		f.ownerTok, f.acceptBody("draft-inbox-7", "21240.0000"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("first accept = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	wantID, _ := first["voucher_id"].(string)
	wantNo, _ := first["voucher_no"].(string)

	// THE SECOND MACHINE. A different amount, so nothing about the CONTENT could
	// catch this — only the draft id.
	rec2, dup := doRaw(t, srv, http.MethodPost, f.base+"/vouchers/purchase",
		f.ownerTok, f.acceptBody("draft-inbox-7", "9999.0000"))
	if rec2.Code != http.StatusConflict {
		t.Fatalf("second accept = %d, want 409 (%s)", rec2.Code, rec2.Body.String())
	}

	// ⛔ THE SHAPE draftAccept.ts PARSES. `detail.code` decides which of three
	// sentences the operator reads; without it a 409 is still a refusal but a
	// generic one.
	detail, ok := dup["detail"].(map[string]any)
	if !ok {
		t.Fatalf("409 body has no `detail` object: %s", rec2.Body.String())
	}
	if got, _ := detail["code"].(string); got != "draft_already_accepted" {
		t.Errorf("detail.code = %q, want %q", got, "draft_already_accepted")
	}
	if got, _ := detail["accepted_voucher_no"].(string); got != wantNo {
		t.Errorf("detail.accepted_voucher_no = %q, want %q", got, wantNo)
	}
	if got, _ := detail["accepted_voucher_id"].(string); got != wantID {
		t.Errorf("detail.accepted_voucher_id = %q, want %q", got, wantID)
	}
	if got, _ := detail["draft_id"].(string); got != "draft-inbox-7" {
		t.Errorf("detail.draft_id = %q, want %q", got, "draft-inbox-7")
	}
	// `error` as well, because http.ts reads THAT key and nothing else to build
	// ApiError.message — without it the operator's message is "request failed: 409".
	if got, _ := dup["error"].(string); got == "" {
		t.Errorf("409 body has no `error` sentence: %s", rec2.Body.String())
	}

	// ⛔ AND THE BOOKS HOLD ONE VOUCHER. A refusal that still posted the payable
	// and the input tax is the defect wearing the fix's clothes.
	if n := f.dayBookCount(t, srv); n != 1 {
		t.Fatalf("day book holds %d vouchers, want 1 — the refused accept posted", n)
	}
}

// TestHandKeyedPurchasesAreUnaffectedByTheDraftGuard is the blast-radius control:
// every other screen in the product posts this body, and it must be untouched.
func TestHandKeyedPurchasesAreUnaffectedByTheDraftGuard(t *testing.T) {
	srv := newServer(t)
	f := newDraftAcceptFixture(t, srv)

	for i, amount := range []string{"100.0000", "200.0000", "300.0000"} {
		rec, _ := doRaw(t, srv, http.MethodPost, f.base+"/vouchers/purchase",
			f.ownerTok, f.acceptBody("", amount))
		if rec.Code != http.StatusCreated {
			t.Fatalf("hand-keyed purchase %d = %d, want 201 (%s)", i+1, rec.Code, rec.Body.String())
		}
	}
	if n := f.dayBookCount(t, srv); n != 3 {
		t.Fatalf("day book holds %d vouchers, want 3", n)
	}
}
